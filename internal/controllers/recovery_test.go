package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jose-Ig/lavalo-backend/internal/operations"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var ctx = context.Background()

func checkpoint(c Command, remaining int64, sequence uint64) *Checkpoint {
	return &Checkpoint{SessionID: c.SessionID, ExecutionID: c.ExecutionID, Revision: c.Revision,
		Sequence: sequence, RemainingMS: remaining, Completed: remaining == 0}
}

func boot(t *testing.T, s *Service, token, id string, cp *Checkpoint) Command {
	t.Helper()
	c, err := s.Bootstrap(ctx, token, BootstrapInput{RequestID: uuid.NewString(), BootID: id, Protocol: 2, Ready: true, Previous: cp})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func active(t *testing.T) (*Service, reservation.Reservation, *time.Time, string, string, Command) {
	t.Helper()
	s, sl, now := fixture(t)
	_, token, err := s.Provision(ctx, sl.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	r := checkedIn(t, s, sl)
	id := uuid.NewString()
	boot(t, s, token, id, nil)
	c, err := s.Poll(ctx, token, uuid.NewString(), id)
	if err != nil || !c.On {
		t.Fatal(c, err)
	}
	if err := s.AckForBoot(ctx, token, id, c.CommandID, true, checkpoint(c, c.EndMS-now.UnixMilli(), 1)); err != nil {
		t.Fatal(err)
	}
	return s, r, now, token, id, c
}

func TestWiFiPauseAndSameBootReconciliation(t *testing.T) {
	s, r, now, token, id, initial := active(t)
	*now = now.Add(3 * time.Minute)
	if err := s.Monitor(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := Snapshot(s.DB, r, *now)
	if err != nil || !v.Frozen || v.RemainingMS != 14*60000 || v.State != operations.Lost {
		t.Fatal(v, err)
	}
	c, err := s.PollReport(ctx, token, uuid.NewString(), id, checkpoint(initial, initial.EndMS-now.UnixMilli(), 2))
	if err != nil || c.EndMS != initial.EndMS || c.CommandID != initial.CommandID {
		t.Fatal(c, err)
	}
	v, err = Snapshot(s.DB, r, *now)
	if err != nil || v.Frozen || v.Status != "active" || v.RemainingMS != 11*60000 {
		t.Fatal(v, err)
	}
	var events int64
	s.DB.Model(&operations.Incident{}).Count(&events)
	if events != 1 {
		t.Fatal(events)
	}
}

func TestPowerRestartRecoversTimeAndBootstrapIsIdempotent(t *testing.T) {
	s, r, now, token, id, initial := active(t)
	*now = now.Add(5 * time.Minute)
	cp := checkpoint(initial, 9*60000, 10)
	*now = now.Add(3 * time.Minute)
	newBoot := uuid.NewString()
	recovered := boot(t, s, token, newBoot, cp)
	if !recovered.On || recovered.RemainingMS != 9*60000 || recovered.EndMS != now.UnixMilli()+9*60000 || recovered.ExecutionID == initial.ExecutionID {
		t.Fatal(recovered)
	}
	*now = now.Add(5 * time.Second)
	repeated := boot(t, s, token, newBoot, cp)
	if repeated.CommandID != recovered.CommandID || repeated.EndMS != recovered.EndMS {
		t.Fatal("bootstrap added time", repeated)
	}
	if err := s.AckForBoot(ctx, token, newBoot, recovered.CommandID, true, checkpoint(recovered, recovered.EndMS-now.UnixMilli(), 1)); err != nil {
		t.Fatal(err)
	}
	v, err := Snapshot(s.DB, r, *now)
	if err != nil || v.Status != "active" || v.EndMS != recovered.EndMS || v.RemainingMS != 9*60000-5000 {
		t.Fatal(v, err)
	}
	if _, err := s.Poll(ctx, token, uuid.NewString(), id); !errors.Is(err, ErrBootstrap) {
		t.Fatal("retired poll accepted", err)
	}
	if _, err := s.Bootstrap(ctx, token, BootstrapInput{BootID: id, Protocol: 2, Ready: true}); !errors.Is(err, ErrStale) {
		t.Fatal("retired boot revived", err)
	}
	if err := s.AckForBoot(ctx, token, id, initial.CommandID, true, cp); !errors.Is(err, ErrBootstrap) {
		t.Fatal(err)
	}
	var stored reservation.Reservation
	s.DB.First(&stored, r.ID)
	if !stored.EndTime.Equal(r.EndTime) {
		t.Fatal("commercial schedule overwritten")
	}
}

func TestRecoveryAfterOriginalEndAndRepeatedOutages(t *testing.T) {
	s, _, now, token, _, initial := active(t)
	*now = now.Add(time.Hour)
	if err := s.Monitor(ctx); err != nil {
		t.Fatal(err)
	}
	first := boot(t, s, token, uuid.NewString(), checkpoint(initial, 4*60000, 40))
	if !first.On || first.RemainingMS != 4*60000 {
		t.Fatal(first)
	}
	id := first.BootID
	if err := s.AckForBoot(ctx, token, id, first.CommandID, true, checkpoint(first, 4*60000, 1)); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	second := boot(t, s, token, uuid.NewString(), checkpoint(first, 2*60000, 20))
	if !second.On || second.RemainingMS != 2*60000 {
		t.Fatal("restored the original time", second)
	}
}

func TestLostRecoveryResponseAndAnotherPowerCutPreserveOnlyUnusedTime(t *testing.T) {
	s, _, now, token, _, initial := active(t)
	*now = now.Add(time.Hour)
	previous := checkpoint(initial, 4*60000, 40)
	unreceived := boot(t, s, token, uuid.NewString(), previous)
	if !unreceived.On {
		t.Fatal(unreceived)
	}
	// The reply never reached the controller: its durable record is still old.
	*now = now.Add(time.Minute)
	retry := boot(t, s, token, uuid.NewString(), previous)
	if !retry.On || retry.RemainingMS != 4*60000 {
		t.Fatal(retry)
	}
}

func TestUnreceivedExtensionThenOfflineUseAndPowerCut(t *testing.T) {
	s, r, now, token, id, initial := active(t)
	*now = now.Add(5 * time.Minute)
	if err := operations.AddPaidTime(s.DB, r.ID, 3); err != nil {
		t.Fatal(err)
	}
	unreceived, err := s.PollReport(ctx, token, uuid.NewString(), id, checkpoint(initial, 9*60000, 2))
	if err != nil || !unreceived.On || unreceived.EndMS != initial.EndMS+3*60000 {
		t.Fatal(unreceived, err)
	}
	// Device kept the old command for another two minutes, then lost power.
	*now = now.Add(2 * time.Minute)
	recovered := boot(t, s, token, uuid.NewString(), checkpoint(initial, 7*60000, 10))
	if !recovered.On || recovered.RemainingMS != 10*60000 {
		t.Fatal(recovered)
	}
}

func TestLostRecoveryResponseUntilItsDeadlineDoesNotConsumeUnappliedTime(t *testing.T) {
	s, _, now, token, _, initial := active(t)
	*now = now.Add(time.Hour)
	previous := checkpoint(initial, 4*60000, 40)
	unreceived := boot(t, s, token, uuid.NewString(), previous)
	*now = now.Add(5 * time.Minute)
	// Same process reconnects with its older durable execution: new GPIO never ran.
	got, err := s.PollReport(ctx, token, uuid.NewString(), unreceived.BootID, previous)
	if err != nil || !got.On || got.RemainingMS != 4*60000 {
		t.Fatal(got, err)
	}
}

func TestOfflineCompletionDoesNotResurrect(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprint(restart), func(t *testing.T) {
			s, r, now, token, id, initial := active(t)
			*now = now.Add(30 * time.Minute)
			if err := s.Monitor(ctx); err != nil {
				t.Fatal(err)
			}
			cp := checkpoint(initial, 0, 50)
			var c Command
			var err error
			if restart {
				c = boot(t, s, token, uuid.NewString(), cp)
			} else {
				c, err = s.PollReport(ctx, token, uuid.NewString(), id, cp)
			}
			if err != nil || c.On || c.State != operations.Completed {
				t.Fatal(c, err)
			}
			v, err := Snapshot(s.DB, r, *now)
			if err != nil || v.Status != "ended" || v.RemainingMS != 0 {
				t.Fatal(v, err)
			}
			if again := boot(t, s, token, uuid.NewString(), checkpoint(initial, 60000, 51)); again.On {
				t.Fatal("terminal session revived", again)
			}
		})
	}
}

func TestRestartMissingInvalidOrForeignCheckpointStaysOff(t *testing.T) {
	for _, kind := range []string{"missing", "foreign", "wrong_revision", "negative", "too_large", "inconsistent_completed"} {
		t.Run(kind, func(t *testing.T) {
			s, _, now, token, _, initial := active(t)
			cp := checkpoint(initial, 60000, 10)
			switch kind {
			case "missing":
				cp = nil
			case "foreign":
				cp.SessionID = uuid.NewString()
			case "wrong_revision":
				cp.Revision++
			case "negative":
				cp.RemainingMS = -1
			case "too_large":
				cp.RemainingMS = MaxSessionMS + 1
			case "inconsistent_completed":
				cp.Completed = true
			}
			*now = now.Add(time.Second)
			c := boot(t, s, token, uuid.NewString(), cp)
			if c.On || c.State != operations.Pending {
				t.Fatal(c)
			}
			c, err := s.Poll(ctx, token, uuid.NewString(), c.BootID)
			if err != nil || c.On {
				t.Fatal("poll bypassed recovery", c, err)
			}
		})
	}
}

func TestRecoveryConflictAndDisabledSpacePreserveTime(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			s, r, now, token, _, initial := active(t)
			*now = now.Add(10 * time.Minute)
			if disabled {
				s.DB.Exec("UPDATE slots SET is_available = ? WHERE id = ?", false, r.SlotID)
			} else {
				s.DB.Create(&reservation.Reservation{SlotID: r.SlotID, StartTime: now.Add(time.Minute), EndTime: now.Add(20 * time.Minute), Status: "confirmed"})
			}
			c := boot(t, s, token, uuid.NewString(), checkpoint(initial, 8*60000, 20))
			if c.On || c.State != operations.Pending || c.RemainingMS != 8*60000 {
				t.Fatal(c)
			}
			v, err := Snapshot(s.DB, r, *now)
			if err != nil || !v.Frozen || v.RemainingMS != 8*60000 {
				t.Fatal(v, err)
			}
		})
	}
}

func TestClosedBookingAndUnactivatedFutureBookingCannotResume(t *testing.T) {
	s, r, _, token, _, initial := active(t)
	s.DB.Model(&r).Update("status", "cancelled")
	c := boot(t, s, token, uuid.NewString(), checkpoint(initial, 60000, 20))
	if c.On || c.State != operations.Closed {
		t.Fatal(c)
	}
	s2, sl, _ := fixture(t)
	_, token2, err := s2.Provision(ctx, sl.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	s2.DB.Create(&reservation.Reservation{SlotID: sl.ID, StartTime: s2.Now().Add(time.Minute), EndTime: s2.Now().Add(time.Hour), Status: "confirmed"})
	c = boot(t, s2, token2, uuid.NewString(), nil)
	if c.On || c.State != "idle" {
		t.Fatal(c)
	}
}

func TestAuthProtocolRotationAndNoPersonalData(t *testing.T) {
	s, sl, _ := fixture(t)
	d, token, err := s.Provision(ctx, sl.PublicID)
	if err != nil || d.TokenHash == token || len(token) != 64 {
		t.Fatal(d, err)
	}
	if _, err = s.Poll(ctx, token, uuid.NewString(), uuid.NewString()); !errors.Is(err, ErrBootstrap) {
		t.Fatal(err)
	}
	if _, err = s.Bootstrap(ctx, "bad", BootstrapInput{Protocol: 2}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	if _, err = s.Bootstrap(ctx, token, BootstrapInput{Protocol: 1}); !errors.Is(err, ErrBootstrap) {
		t.Fatal(err)
	}
	checkedIn(t, s, sl)
	id := uuid.NewString()
	boot(t, s, token, id, nil)
	c, err := s.Poll(ctx, token, uuid.NewString(), id)
	if err != nil || !c.On {
		t.Fatal(c, err)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), token) {
		t.Fatal("personal data exposed")
	}
	newer, newToken, err := s.Provision(ctx, sl.PublicID)
	if err != nil || newer.ID != d.ID || token == newToken {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx, token, uuid.NewString(), id); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	s.DB.Model(&newer).Update("enabled", false)
	if _, err = s.Bootstrap(ctx, newToken, BootstrapInput{Protocol: 2}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestEligibilityFailsClosed(t *testing.T) {
	for _, state := range []string{"unpaid", "not_scanned", "too_early", "cancelled", "slot_disabled", "overlap"} {
		t.Run(state, func(t *testing.T) {
			s, sl, _ := fixture(t)
			r := checkedIn(t, s, sl)
			_, token, err := s.Provision(ctx, sl.PublicID)
			if err != nil {
				t.Fatal(err)
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
			id := uuid.NewString()
			boot(t, s, token, id, nil)
			c, err := s.Poll(ctx, token, uuid.NewString(), id)
			if err != nil || c.On {
				t.Fatal(c, err)
			}
		})
	}
}

func TestHTTPBootstrapBoundsAndAck(t *testing.T) {
	s, sl, _ := fixture(t)
	_, token, err := s.Provision(ctx, sl.PublicID)
	if err != nil {
		t.Fatal(err)
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
	id := uuid.NewString()
	body := fmt.Sprintf(`{"request_id":%q,"boot_id":%q,"protocol":2,"ready":true}`, uuid.NewString(), id)
	if w := call("bootstrap", body, "bad"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("bootstrap", `{}`, token); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call("bootstrap", `{"padding":"`+strings.Repeat("a", 3000)+`"}`, token); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := call("bootstrap", body, token)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		Data Command `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if w = call("ack", fmt.Sprintf(`{"command_id":%q,"boot_id":%q,"on":false}`, v.Data.CommandID, id), token); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("poll", fmt.Sprintf(`{"request_id":%q,"boot_id":%q}`, uuid.NewString(), uuid.NewString()), token); w.Code != 409 {
		t.Fatal(w.Code)
	}
}
