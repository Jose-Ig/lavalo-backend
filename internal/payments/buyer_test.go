package payments

import (
	"context"
	"encoding/json"
	"github.com/Jose-Ig/lavalo-backend/internal/bookings"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"testing"
	"time"
)

func buyerBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	return body
}
func assertBuyer(t *testing.T, raw string) {
	t.Helper()
	body := buyerBody(t, raw)
	payer, ok := body["payer"].(map[string]any)
	if !ok {
		t.Fatal("missing payer")
	}
	if payer["email"] != "buyer@example.com" || payer["first_name"] != "María José" || payer["last_name"] != "Pérez Gómez" {
		t.Fatal("incorrect payer fields")
	}
	phone, ok := payer["phone"].(map[string]any)
	if !ok || phone["area_code"] != "351" || phone["number"] != "1234567" {
		t.Fatal("incorrect phone split")
	}
	if _, exists := body["additional_info"]; exists {
		t.Fatal("guest must not have invented registration date")
	}
	config := body["config"].(map[string]any)
	if config["statement_descriptor"] != "UASH" {
		t.Fatal("missing business descriptor")
	}
}
func TestBuyerSnapshotSurvivesRetryAndExtension(t *testing.T) {
	s, f, id, token := fixture(t)
	ctx := context.Background()
	if err := s.DB.Model(&reservation.Reservation{}).Where("id = ?", id).Updates(map[string]any{"first_name": "María José", "last_name": "Pérez Gómez", "email": "buyer@example.com", "phone": "+543511234567", "phone_area_code": "351"}).Error; err != nil {
		t.Fatal(err)
	}
	f.fail = true
	if _, err := s.Start(ctx, id, token); err == nil {
		t.Fatal("lost response expected")
	}
	var checkout Checkout
	if err := s.DB.Where("reservation_id = ?", id).First(&checkout).Error; err != nil {
		t.Fatal(err)
	}
	original := checkout.RequestBody
	assertBuyer(t, original)
	// Even a subsequent configuration change must not mutate an idempotent retry.
	s.Config.StatementDescriptor = "OTHER STORE"
	f.fail = false
	v, err := s.Start(ctx, id, token)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.bodies) != 2 || f.bodies[0] != original || f.bodies[1] != original {
		t.Fatal("retry mutated buyer snapshot")
	}
	if err = s.Apply(ctx, approved(s, v.Checkout)); err != nil {
		t.Fatal(err)
	}
	var parent reservation.Reservation
	if err = s.DB.First(&parent, id).Error; err != nil {
		t.Fatal(err)
	}
	now := parent.StartTime.Add(time.Minute)
	s.Now = func() time.Time { return now }
	s.Bookings.Now = s.Now
	var assigned slot.Slot
	if err = s.DB.First(&assigned, parent.SlotID).Error; err != nil {
		t.Fatal(err)
	}
	day := parent.StartTime.In(bookings.Location).Format("2006-01-02")
	if _, err = s.Bookings.VerifySlotQR(ctx, bookings.ConfirmationCode(parent), "uash://slot/"+assigned.PublicID, day); err != nil {
		t.Fatal(err)
	}
	child, secret, err := s.Bookings.CreateExtension(ctx, bookings.ConfirmationCode(parent), day, 3)
	if err != nil {
		t.Fatal(err)
	}
	s.Config.StatementDescriptor = ""
	v, err = s.Start(ctx, child.ID, secret)
	if err != nil {
		t.Fatal(err)
	}
	assertBuyer(t, v.RequestBody)
}
func TestHistoricalGuestsDoNotGetInventedBuyerFields(t *testing.T) {
	now := time.Now()
	c := Checkout{AmountCents: 25000, ExpiresAt: now.Add(time.Minute)}
	for _, r := range []reservation.Reservation{{Name: "Old Full Name", Phone: "+543511234567"}, {Name: "Old Full Name", Email: "buyer@example.com", Phone: "+543511234567"}} {
		raw, err := orderRequest(c, Config{}, now, r)
		if err != nil {
			t.Fatal(err)
		}
		body := buyerBody(t, raw)
		payer, exists := body["payer"]
		if r.Email == "" {
			if exists {
				t.Fatal("payer requires real email")
			}
			continue
		}
		data := payer.(map[string]any)
		if len(data) != 1 || data["email"] != r.Email {
			t.Fatal("legacy name or area guessed")
		}
	}
}
func TestStatementDescriptorConfiguration(t *testing.T) {
	t.Setenv("MP_STATEMENT_DESCRIPTOR", " UASH ")
	cfg := ConfigFromEnv()
	if cfg.descriptor() != "UASH" {
		t.Fatal("env not loaded")
	}
	cfg = Config{Enabled: true, AccessToken: "test", WebhookSecret: "test", CollectorID: 123, PublicURL: "https://api.example.com"}
	if !cfg.Ready() {
		t.Fatal("default descriptor rejected")
	}
	cfg.StatementDescriptor = "1234567890123"
	if !cfg.Ready() {
		t.Fatal("13 characters should work")
	}
	cfg.StatementDescriptor += "X"
	if cfg.Ready() {
		t.Fatal("over 13 characters accepted")
	}
}
