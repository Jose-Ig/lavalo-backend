package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Service, *slot.Slot, *time.Time) {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := db.DB()
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	if e = db.AutoMigrate(&Device{}, &reservation.Reservation{}, &slot.Slot{}); e != nil {
		t.Fatal(e)
	}
	sl := &slot.Slot{Label: "Espacio N° 1", IsAvailable: true}
	if e = db.Create(sl).Error; e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC)
	s := New(db)
	s.Now = func() time.Time { return now }
	return s, sl, &now
}
func checkedIn(t *testing.T, s *Service, sl *slot.Slot) reservation.Reservation {
	t.Helper()
	n := s.Now()
	r := reservation.Reservation{SlotID: sl.ID, Status: "confirmed", StartTime: n.Add(-time.Minute), EndTime: n.Add(14 * time.Minute), QRVerifiedAt: &n, Email: "private@example.com", Phone: "private-phone"}
	if e := s.DB.Create(&r).Error; e != nil {
		t.Fatal(e)
	}
	return r
}
func TestAuthorizationProvisionRotationAndNoPII(t *testing.T) {
	s, sl, _ := fixture(t)
	ctx := context.Background()
	d, token, e := s.Provision(ctx, sl.PublicID)
	if e != nil {
		t.Fatal(e)
	}
	if d.TokenHash == token || len(token) != 64 {
		t.Fatal("plaintext credential stored")
	}
	if _, e = s.Poll(ctx, "bad", uuid.NewString(), uuid.NewString()); !errors.Is(e, ErrUnauthorized) {
		t.Fatal(e)
	}
	c, e := s.Poll(ctx, token, uuid.NewString(), uuid.NewString())
	if e != nil || c.On {
		t.Fatal(c, e)
	}
	checkedIn(t, s, sl)
	c, e = s.Poll(ctx, token, uuid.NewString(), uuid.NewString())
	if e != nil || !c.On {
		t.Fatal(c, e)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), token) {
		t.Fatal("private data exposed")
	}
	newer, newToken, e := s.Provision(ctx, sl.PublicID)
	if e != nil || newer.ID != d.ID || newToken == token {
		t.Fatal("rotation failed", e)
	}
	if _, e = s.Poll(ctx, token, uuid.NewString(), uuid.NewString()); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("old token accepted", e)
	}
	if _, e = s.Poll(ctx, newToken, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	s.DB.Model(&newer).Update("enabled", false)
	if _, e = s.Poll(ctx, newToken, uuid.NewString(), uuid.NewString()); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("disabled controller accepted", e)
	}
}
func TestPollingIdempotenceAcknowledgementRestartAndCutoff(t *testing.T) {
	s, sl, now := fixture(t)
	ctx := context.Background()
	_, token, e := s.Provision(ctx, sl.PublicID)
	if e != nil {
		t.Fatal(e)
	}
	r := checkedIn(t, s, sl)
	boot := uuid.NewString()
	nonce := uuid.NewString()
	if status, e := Activation(s.DB, r, *now); e != nil || status != "controller_offline" {
		t.Fatal(status, e)
	}
	first, e := s.Poll(ctx, token, nonce, boot)
	if e != nil || !first.On || first.RequestID != nonce || first.EndMS != r.EndTime.UnixMilli() {
		t.Fatal(first, e)
	}
	second, e := s.Poll(ctx, token, uuid.NewString(), boot)
	if e != nil || second.CommandID != first.CommandID || second.Revision != first.Revision {
		t.Fatal("poll restarted lease", first, second, e)
	}
	if status, _ := Activation(s.DB, r, *now); status != "awaiting_controller" {
		t.Fatal(status)
	}
	if e = s.Ack(ctx, token, first.CommandID, false); !errors.Is(e, ErrStale) {
		t.Fatal("wrong output state accepted", e)
	}
	if e = s.Ack(ctx, token, first.CommandID, true); e != nil {
		t.Fatal(e)
	}
	if e = s.Ack(ctx, token, first.CommandID, true); e != nil {
		t.Fatal("duplicate acknowledgement rejected", e)
	}
	if status, _ := Activation(s.DB, r, *now); status != "active" {
		t.Fatal(status)
	}
	*now = now.Add(13 * time.Second)
	if status, _ := Activation(s.DB, r, *now); status != "controller_offline" {
		t.Fatal(status)
	}
	restarted, e := s.Poll(ctx, token, uuid.NewString(), uuid.NewString())
	if e != nil || restarted.CommandID == first.CommandID || restarted.Revision <= first.Revision {
		t.Fatal("restart reused prior command", e)
	}
	if status, _ := Activation(s.DB, r, *now); status != "awaiting_controller" {
		t.Fatal("old boot ack reused", status)
	}
	if e = s.Ack(ctx, token, first.CommandID, true); !errors.Is(e, ErrStale) {
		t.Fatal("stale boot acknowledgement accepted", e)
	}
	*now = r.EndTime
	off, e := s.Poll(ctx, token, uuid.NewString(), boot)
	if e != nil || off.On || off.EndMS != 0 {
		t.Fatal("expired command on", off, e)
	}
	if e = s.Ack(ctx, token, restarted.CommandID, true); !errors.Is(e, ErrStale) {
		t.Fatal("expired ack accepted", e)
	}
	if e = s.Ack(ctx, token, off.CommandID, false); e != nil {
		t.Fatal(e)
	}
}
func TestEligibilityFailsClosed(t *testing.T) {
	for _, state := range []string{"unpaid", "not_scanned", "too_early", "cancelled", "slot_disabled", "overlap"} {
		t.Run(state, func(t *testing.T) {
			s, sl, _ := fixture(t)
			r := checkedIn(t, s, sl)
			_, token, e := s.Provision(context.Background(), sl.PublicID)
			if e != nil {
				t.Fatal(e)
			}
			switch state {
			case "unpaid":
				s.DB.Model(&r).Update("status", "pending")
			case "not_scanned":
				s.DB.Model(&r).Update("qr_verified_at", nil)
			case "too_early":
				s.DB.Model(&r).Update("start_time", s.Now().Add(time.Minute))
			case "cancelled":
				s.DB.Model(&r).Update("status", "cancelled")
			case "slot_disabled":
				s.DB.Model(sl).Update("is_available", false)
			case "overlap":
				r.ID = 0
				s.DB.Create(&r)
			}
			c, e := s.Poll(context.Background(), token, uuid.NewString(), uuid.NewString())
			if e != nil || c.On {
				t.Fatal("ineligible equipment enabled", c, e)
			}
		})
	}
}
func TestHTTPBoundsAuthAndAck(t *testing.T) {
	s, sl, _ := fixture(t)
	_, token, e := s.Provision(context.Background(), sl.PublicID)
	if e != nil {
		t.Fatal(e)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Register(router.Group("/api/v1"), s)
	call := func(path, body, secret string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/controllers/"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	body := fmt.Sprintf(`{"request_id":%q,"boot_id":%q}`, uuid.NewString(), uuid.NewString())
	if w := call("poll", body, "bad"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("poll", `{}`, token); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call("poll", `{"padding":"`+strings.Repeat("a", 1500)+`"}`, token); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := call("poll", body, token)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		Data Command `json:"data"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if w = call("ack", fmt.Sprintf(`{"command_id":%q}`, v.Data.CommandID), token); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w = call("ack", fmt.Sprintf(`{"command_id":%q,"on":false}`, v.Data.CommandID), token); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
}
