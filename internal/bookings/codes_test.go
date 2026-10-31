package bookings

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func confirmedFixture(t *testing.T) (*Service, *reservation.Reservation, string) {
	s, i := fixture(t)
	if e := s.DB.AutoMigrate(&NotificationIntent{}); e != nil {
		t.Fatal(e)
	}
	i.Email = "guest@example.com"
	i.WhatsAppOptIn = true
	r, token, e := s.Create(context.Background(), i)
	if e != nil {
		t.Fatal(e)
	}
	r.Status = "confirmed"
	r.ExpiresAt = nil
	e = s.DB.Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(r).Updates(map[string]any{"status": "confirmed", "expires_at": nil}).Error; e != nil {
			return e
		}
		return EnsureConfirmation(tx, r)
	})
	if e != nil {
		t.Fatal(e)
	}
	return s, r, token
}
func TestIndependentCodesAndIdempotentNotificationIntents(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	ctx := context.Background()
	code := ConfirmationCode(*r)
	if code == "" || len(code) != 24 {
		t.Fatal("invalid confirmation code")
	}
	for i := 0; i < 2; i++ {
		if e := s.DB.Transaction(func(tx *gorm.DB) error { return EnsureConfirmation(tx, r) }); e != nil {
			t.Fatal(e)
		}
	}
	var n int64
	s.DB.Model(&NotificationIntent{}).Count(&n)
	if n != 2 {
		t.Fatal("duplicate notifications", n)
	}
	var channels []string
	s.DB.Model(&NotificationIntent{}).Order("channel").Pluck("channel", &channels)
	if strings.Join(channels, ",") != "sms,whatsapp" {
		t.Fatal("expected SMS plus WhatsApp, never email", channels)
	}
	lookup, e := s.LookupCode(ctx, strings.ToLower(code))
	if e != nil || lookup.ID != r.ID || lookup.CanVerifyQR {
		t.Fatal(lookup, e)
	}
	i := Input{SlotID: r.SlotID, StartTime: r.EndTime, Duration: r.DurationMinutes, Name: r.Name, Phone: r.Phone, Plate: r.VehiclePlate, Model: r.VehicleModel} // Same person, another non-overlapping reservation.
	second, _, e := s.Create(ctx, i)
	if e != nil {
		t.Fatal(e)
	}
	second.Status = "confirmed"
	if e = s.DB.Model(second).Update("status", "confirmed").Error; e != nil {
		t.Fatal(e)
	}
	if e = s.DB.Transaction(func(tx *gorm.DB) error { return EnsureConfirmation(tx, second) }); e != nil {
		t.Fatal(e)
	}
	if ConfirmationCode(*second) == code {
		t.Fatal("two reservations share a code")
	}
	found, e := s.LookupCode(ctx, ConfirmationCode(*second))
	if e != nil || found.ID != second.ID {
		t.Fatal(found, e)
	}
}
func TestUnknownAndUnpaidCodesAreRejected(t *testing.T) {
	s, i := fixture(t)
	r, _, e := s.Create(context.Background(), i)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.LookupCode(context.Background(), ConfirmationCode(*r)); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal("unpaid code accepted", e)
	}
	if _, e = s.LookupCode(context.Background(), "UASH-AAAA-AAAA-AAAA-AAAA"); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal(e)
	}
}
func TestQRRequiresCorrectPaidReservationSpaceAndTime(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	code := ConfirmationCode(*r)
	ctx := context.Background()
	if _, e := s.VerifySlotQR(ctx, code, "uash://slot/1"); !errors.Is(e, ErrConflict) {
		t.Fatal("early access accepted", e)
	}
	now := r.StartTime
	s.Now = func() time.Time { return now }
	if _, e := s.VerifySlotQR(ctx, code, "uash://slot/2"); !errors.Is(e, ErrConflict) {
		t.Fatal("wrong space accepted", e)
	}
	if _, e := s.VerifySlotQR(ctx, code, "https://evil.example/1"); !errors.Is(e, ErrInvalid) {
		t.Fatal("arbitrary QR accepted", e)
	}
	first, e := s.VerifySlotQR(ctx, code, "uash://slot/1")
	if e != nil || first.QRVerifiedAt == nil || first.ActivationStatus != "awaiting_controller" {
		t.Fatal(first, e)
	}
	second, e := s.VerifySlotQR(ctx, code, "uash://slot/1")
	if e != nil || !second.QRVerifiedAt.Equal(*first.QRVerifiedAt) {
		t.Fatal("QR replay changed check-in", e)
	}
	now = r.EndTime
	if _, e = s.VerifySlotQR(ctx, code, "uash://slot/1"); !errors.Is(e, ErrConflict) {
		t.Fatal("expired access accepted", e)
	}
	now = r.StartTime
	s.DB.Model(r).Update("status", "cancelled")
	if _, e = s.VerifySlotQR(ctx, code, "uash://slot/1"); !errors.Is(e, ErrConflict) {
		t.Fatal("cancelled access accepted", e)
	}
}
func TestCodeEndpointLimitsGuesses(t *testing.T) {
	s, _ := fixture(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCodeRoutes(router.Group("/api/v1"), s)
	for i := 0; i < 61; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/reservations/lookup", strings.NewReader(`{"code":"unknown"}`))
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if i < 60 && w.Code != 404 {
			t.Fatal(w.Code)
		}
		if i == 60 && w.Code != 429 {
			t.Fatal("guess limiter failed", w.Code)
		}
	}
}
func TestWhatsAppRequiresOptIn(t *testing.T) {
	s, i := fixture(t)
	s.DB.AutoMigrate(&NotificationIntent{})
	i.Email = ""
	i.WhatsAppOptIn = false
	r, _, e := s.Create(context.Background(), i)
	if e != nil {
		t.Fatal(e)
	}
	r.Status = "confirmed"
	s.DB.Model(r).Update("status", "confirmed")
	if e = s.DB.Transaction(func(tx *gorm.DB) error { return EnsureConfirmation(tx, r) }); e != nil {
		t.Fatal(e)
	}
	var n int64
	s.DB.Model(&NotificationIntent{}).Count(&n)
	if n != 1 {
		t.Fatal("expected SMS by default", n)
	}
	var intent NotificationIntent
	if err := s.DB.First(&intent).Error; err != nil || intent.Channel != "sms" {
		t.Fatal("expected only SMS without WhatsApp consent", intent.Channel, err)
	}
}
