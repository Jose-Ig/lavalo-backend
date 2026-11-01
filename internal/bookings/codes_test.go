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
	if code == "" || len(code) != 13 {
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
	if strings.Join(channels, ",") != "email,whatsapp" {
		t.Fatal("expected WhatsApp plus optional email, never SMS", channels)
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
	if _, e := s.VerifySlotQR(ctx, code, testSlotQR(t, s)); !errors.Is(e, ErrConflict) {
		t.Fatal("early access accepted", e)
	}
	now := r.StartTime
	s.Now = func() time.Time { return now }
	if _, e := s.VerifySlotQR(ctx, code, "uash://slot/aef3954a-a1d3-42d0-978f-e50e6f81cb03"); !errors.Is(e, ErrConflict) {
		t.Fatal("wrong space accepted", e)
	}
	if _, e := s.VerifySlotQR(ctx, code, "https://evil.example/1"); !errors.Is(e, ErrInvalid) {
		t.Fatal("arbitrary QR accepted", e)
	}
	first, e := s.VerifySlotQR(ctx, code, testSlotQR(t, s))
	if e != nil || first.QRVerifiedAt == nil || first.ActivationStatus != "awaiting_controller" {
		t.Fatal(first, e)
	}
	second, e := s.VerifySlotQR(ctx, code, testSlotQR(t, s))
	if e != nil || !second.QRVerifiedAt.Equal(*first.QRVerifiedAt) {
		t.Fatal("QR replay changed check-in", e)
	}
	now = r.EndTime
	if _, e = s.VerifySlotQR(ctx, code, testSlotQR(t, s)); !errors.Is(e, ErrConflict) {
		t.Fatal("expired access accepted", e)
	}
	now = r.StartTime
	s.DB.Model(r).Update("status", "cancelled")
	if _, e = s.VerifySlotQR(ctx, code, testSlotQR(t, s)); !errors.Is(e, ErrConflict) {
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
func TestWhatsAppIsDefaultAndEmailIsOptional(t *testing.T) {
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
		t.Fatal("expected WhatsApp by default", n)
	}
	var intent NotificationIntent
	if err := s.DB.First(&intent).Error; err != nil || intent.Channel != "whatsapp" {
		t.Fatal("expected only WhatsApp without an email", intent.Channel, err)
	}
}

func TestDailyCodeRequiresCorrectDateForLookupAndQR(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	code := ConfirmationCode(*r)
	day := r.StartTime.In(Location).Format("2006-01-02")
	wrong := r.StartTime.In(Location).AddDate(0, 0, 1).Format("2006-01-02")
	if _, err := s.LookupCode(context.Background(), code, wrong); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("wrong date accepted", err)
	}
	if _, err := s.LookupCode(context.Background(), code, "2026-02-30"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("invalid date accepted", err)
	}
	now := r.StartTime
	s.Now = func() time.Time { return now }
	if _, err := s.VerifySlotQR(context.Background(), code, testSlotQR(t, s), wrong); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("QR ignored date", err)
	}
	got, err := s.VerifySlotQR(context.Background(), code, testSlotQR(t, s), day)
	if err != nil || got.ReservationDate != day || got.ReservationNumber != r.Number() {
		t.Fatal(got, err)
	}
	// The same short code can have an independent hash on another day.
	if dailyCodeHash(day, code) == dailyCodeHash(wrong, code) {
		t.Fatal("hash is not date scoped")
	}
}
func TestLegacyCodesRemainStable(t *testing.T) {
	s, i := fixture(t)
	s.DB.AutoMigrate(&NotificationIntent{})
	r, _, err := s.Create(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	legacy := ConfirmationCode(*r)
	hash := codeHash(legacy)
	r.Status = "confirmed"
	r.CodeHash = &hash
	if err = s.DB.Save(r).Error; err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Transaction(func(tx *gorm.DB) error { return EnsureConfirmation(tx, r) }); err != nil {
		t.Fatal(err)
	}
	if ConfirmationCode(*r) != legacy || len(legacy) != 24 {
		t.Fatal("legacy code changed")
	}
	got, err := s.LookupCode(context.Background(), legacy)
	if err != nil || got.ID != r.ID {
		t.Fatal(got, err)
	}
}
func TestDailyCollisionSelectsAnotherStableCode(t *testing.T) {
	s, i := fixture(t)
	s.DB.AutoMigrate(&NotificationIntent{})
	r, _, err := s.Create(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	candidate := *r
	candidate.CodeVersion = 2
	firstCode := ConfirmationCode(candidate)
	hash := dailyCodeHash(r.StartTime.In(Location).Format("2006-01-02"), firstCode)
	// Reserve the first candidate, simulating another code already allocated that day.
	other := reservation.Reservation{SlotID: r.SlotID, StartTime: r.EndTime, CodeHash: &hash, CodeVersion: 2, Status: "cancelled"}
	if err = s.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	r.Status = "confirmed"
	if err = s.DB.Model(r).Update("status", "confirmed").Error; err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Transaction(func(tx *gorm.DB) error { return EnsureConfirmation(tx, r) }); err != nil {
		t.Fatal(err)
	}
	if r.CodeNonce == 0 || ConfirmationCode(*r) == firstCode {
		t.Fatal("collision was not resolved")
	}
	code := ConfirmationCode(*r)
	if err = s.DB.Transaction(func(tx *gorm.DB) error { return EnsureConfirmation(tx, r) }); err != nil {
		t.Fatal(err)
	}
	if ConfirmationCode(*r) != code {
		t.Fatal("retry changed allocated code")
	}
}
func TestShortCodeKeepsLeadingZeroAndLocalReservationNumber(t *testing.T) {
	r := reservation.Reservation{ID: 10, AccessTokenHash: strings.Repeat("a", 64), CodeVersion: 2}
	found := false
	for nonce := 0; nonce < 1000; nonce++ {
		r.CodeNonce = nonce
		code := ConfirmationCode(r)
		if len(code) != 13 {
			t.Fatal("code length", code)
		}
		if code[0] == '0' {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("leading zero was lost")
	}
	r.StartTime = time.Date(2026, 11, 1, 1, 30, 0, 0, time.UTC)
	if !strings.HasSuffix(ConfirmationCode(r), "-311026") {
		t.Fatal("wrong local code suffix", ConfirmationCode(r))
	}
	if r.Number() != "31102026-2230" {
		t.Fatal("wrong Argentina date/time", r.Number())
	}
}

func TestObsoletePendingSMSIsDisabledAndEmailRemainsOptional(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	sms := NotificationIntent{ReservationID: r.ID, Channel: "sms", Status: "pending_configuration"}
	if err := s.DB.Create(&sms).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Transaction(func(tx *gorm.DB) error { return EnsureConfirmation(tx, r) }); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.First(&sms, sms.ID).Error; err != nil || sms.Status != "cancelled_channel_disabled" {
		t.Fatal("obsolete SMS left queued", sms.Status, err)
	}
	var queued int64
	s.DB.Model(&NotificationIntent{}).Where("status = ?", "pending_configuration").Count(&queued)
	if queued != 2 {
		t.Fatal("expected only WhatsApp and email pending", queued)
	}
}
func TestOptionalEmailIsValidatedAndStored(t *testing.T) {
	s, i := fixture(t)
	for _, email := range []string{"not-an-email", "Name <guest@example.com>", "guest@example.com,other@example.com"} {
		i.Email = email
		if _, _, err := s.Create(context.Background(), i); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid email accepted", err)
		}
	}
	i.Email = "  guest@example.com  "
	r, token, err := s.Create(context.Background(), i)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.Get(context.Background(), r.ID, token)
	if err != nil || stored.Email != "guest@example.com" || !stored.WhatsAppOptIn {
		t.Fatal("email/default WhatsApp not persisted", err)
	}
}

func testSlotQR(t *testing.T, s *Service) string {
	t.Helper()
	var sl struct{ PublicID string }
	if err := s.DB.Table("slots").Select("public_id").Where("id = ?", 1).Take(&sl).Error; err != nil {
		t.Fatal(err)
	}
	return "uash://slot/" + sl.PublicID
}

func TestSlotQRRejectsPredictableAndMalformedIdentifiers(t *testing.T) {
	for _, qr := range []string{
		"uash://slot/1", "uash://slot/0", "uash://slot/00000000-0000-0000-0000-000000000000",
		"uash://slot/aef3954a-a1d3-42d0-978f-e50e6f81cb03?", "uash://slot/aef3954a-a1d3-42d0-978f-e50e6f81cb03/",
		"https://slot/aef3954a-a1d3-42d0-978f-e50e6f81cb03", "uash://slot/aef3954aa1d342d0978fe50e6f81cb03",
	} {
		if _, err := parseSlotQR(qr); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted QR %q: %v", qr, err)
		}
	}
}

func TestDatedCodesKeepExistingSixDigitCodesWorking(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	code := ConfirmationCode(*r)
	day := r.StartTime.In(Location).Format("2006-01-02")
	if len(code) != 13 || code[6] != '-' || code[7:] != r.StartTime.In(Location).Format("020106") {
		t.Fatal("invalid dated code", code)
	}
	if got, err := s.LookupCode(context.Background(), code[:6], day); err != nil || got.ID != r.ID || got.Code != code {
		t.Fatal("historical short code rejected", got, err)
	}
	if _, err := s.LookupCode(context.Background(), code[:7]+"010100", day); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("wrong suffix accepted", err)
	}
}
