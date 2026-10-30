package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Jose-Ig/lavalo-backend/internal/bookings"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakeGateway struct {
	mu       sync.Mutex
	calls    int
	fail     bool
	payments []ProviderPayment
}

func (f *fakeGateway) CreatePreference(context.Context, Checkout) (Preference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return Preference{}, ErrUnavailable
	}
	return Preference{ID: "pref", URL: "https://www.mercadopago.com.ar/checkout/test"}, nil
}
func (f *fakeGateway) GetPayment(context.Context, string) (ProviderPayment, error) {
	return f.payments[0], nil
}
func (f *fakeGateway) SearchPayments(context.Context, string) ([]ProviderPayment, error) {
	return f.payments, nil
}
func fixture(t *testing.T) (*Service, *fakeGateway, uint, string) {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	if e = db.AutoMigrate(&reservation.Reservation{}, &slot.Slot{}, &Checkout{}, &Receipt{}); e != nil {
		t.Fatal(e)
	}
	db.Create(&slot.Slot{ID: 1, Label: "One", IsAvailable: true})
	now := time.Date(2026, 10, 30, 11, 0, 0, 0, bookings.Location)
	s := New(db, Config{Enabled: true, CollectorID: 123, AccessToken: "test", WebhookSecret: "secret", PublicURL: "https://api.example.com"})
	s.Now = func() time.Time { return now }
	s.Bookings.Now = s.Now
	r, token, e := s.Bookings.Create(context.Background(), bookings.Input{SlotID: 1, StartTime: now.Add(time.Hour), Duration: 60, Name: "User", Phone: "1234567890", Plate: "ABC123", Model: "Car"})
	if e != nil {
		t.Fatal(e)
	}
	f := &fakeGateway{}
	s.Gateway = f
	return s, f, r.ID, token
}
func approved(s *Service, c Checkout) ProviderPayment {
	return ProviderPayment{ID: 99, Reference: c.Reference, Amount: json.Number("5000.00"), Currency: "ARS", CollectorID: 123, Status: "approved", UpdatedAt: s.Now()}
}
func TestCheckoutReuseAndPrivateAccess(t *testing.T) {
	s, f, id, token := fixture(t)
	ctx := context.Background()
	if _, e := s.Start(ctx, id, "bad"); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.Start(ctx, id, token); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil && !errors.Is(e, ErrUnavailable) {
			t.Fatal(e)
		}
	}
	if _, e := s.Start(ctx, id, token); e != nil {
		t.Fatal(e)
	}
	if f.calls != 1 {
		t.Fatalf("created %d preferences", f.calls)
	}
	var c Checkout
	s.DB.First(&c)
	if c.AmountCents != 500000 {
		t.Fatal(c.AmountCents)
	}
}
func TestAmbiguousCreationNeverRepeatsPOST(t *testing.T) {
	s, f, id, token := fixture(t)
	f.fail = true
	for i := 0; i < 2; i++ {
		if _, e := s.Start(context.Background(), id, token); !errors.Is(e, ErrUnavailable) {
			t.Fatal(e)
		}
	}
	if f.calls != 1 {
		t.Fatal(f.calls)
	}
}
func TestConfirmationReplayRefundAndStaleEvent(t *testing.T) {
	s, f, id, token := fixture(t)
	ctx := context.Background()
	v, e := s.Start(ctx, id, token)
	if e != nil {
		t.Fatal(e)
	}
	p := approved(s, v.Checkout)
	f.payments = []ProviderPayment{p}
	for i := 0; i < 2; i++ {
		if e = s.Apply(ctx, p); e != nil {
			t.Fatal(e)
		}
	}
	r, e := s.Bookings.Get(ctx, id, token)
	if e != nil || r.Status != "confirmed" {
		t.Fatal(r, e)
	}
	var count int64
	s.DB.Model(&Receipt{}).Count(&count)
	if count != 1 {
		t.Fatal(count)
	}
	if e = s.Bookings.Cancel(ctx, id, token); !errors.Is(e, bookings.ErrConflict) {
		t.Fatal("paid cancellation allowed", e)
	}
	p.Status = "refunded"
	p.UpdatedAt = p.UpdatedAt.Add(time.Second)
	if e = s.Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	if e = s.Apply(ctx, f.payments[0]); e != nil {
		t.Fatal(e)
	}
	r, _ = s.Bookings.Get(ctx, id, token)
	if r.Status != "cancelled" {
		t.Fatal(r.Status)
	}
}
func TestMismatchCannotConfirm(t *testing.T) {
	for _, field := range []string{"amount", "currency", "collector", "live", "reference"} {
		t.Run(field, func(t *testing.T) {
			s, _, id, token := fixture(t)
			v, e := s.Start(context.Background(), id, token)
			if e != nil {
				t.Fatal(e)
			}
			p := approved(s, v.Checkout)
			switch field {
			case "amount":
				p.Amount = "0.01"
			case "currency":
				p.Currency = "USD"
			case "collector":
				p.CollectorID = 999
			case "live":
				p.LiveMode = true
			case "reference":
				p.Reference = "wrong"
			}
			if e = s.Apply(context.Background(), p); e == nil {
				t.Fatal("mismatch accepted")
			}
			r, _ := s.Bookings.Get(context.Background(), id, token)
			if r.Status != "pending" {
				t.Fatal(r.Status)
			}
		})
	}
}
func TestLateAndDuplicatePaymentsNeedReview(t *testing.T) {
	for _, scenario := range []string{"late", "cancelled", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, id, token := fixture(t)
			ctx := context.Background()
			v, e := s.Start(ctx, id, token)
			if e != nil {
				t.Fatal(e)
			}
			p := approved(s, v.Checkout)
			if scenario == "late" {
				now := s.Now().Add(11 * time.Minute)
				s.Now = func() time.Time { return now }
				s.Bookings.Now = s.Now
			}
			if scenario == "cancelled" {
				if e = s.Bookings.Cancel(ctx, id, token); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.Apply(ctx, p); e != nil {
				t.Fatal(e)
			}
			if scenario == "duplicate" {
				p.ID++
				if e = s.Apply(ctx, p); e != nil {
					t.Fatal(e)
				}
			}
			var c Checkout
			s.DB.First(&c, v.ID)
			if c.State != "review" {
				t.Fatal(c.State)
			}
			r, _ := s.Bookings.Get(ctx, id, token)
			if scenario != "duplicate" && r.Status == "confirmed" {
				t.Fatal("late booking confirmed")
			}
		})
	}
}
func TestSignedURLAndReconciliation(t *testing.T) {
	req := httptest.NewRequest("POST", "/webhook?data.id=99", nil)
	req.Header.Set("x-request-id", "request")
	mac := hmac.New(sha256.New, []byte("secret"))
	fmt.Fprint(mac, "id:99;request-id:request;ts:1704908010;")
	req.Header.Set("x-signature", "ts=1704908010,v1="+hex.EncodeToString(mac.Sum(nil)))
	if !VerifySignature(req, "secret") {
		t.Fatal("valid signature rejected")
	}
	req.URL.RawQuery = "data.id=100"
	if VerifySignature(req, "secret") {
		t.Fatal("unsigned id accepted")
	}
	s, f, id, token := fixture(t)
	v, e := s.Start(context.Background(), id, token)
	if e != nil {
		t.Fatal(e)
	}
	f.payments = []ProviderPayment{approved(s, v.Checkout)}
	status, e := s.Status(context.Background(), id, token)
	if e != nil || !status.Verified || status.State != "completed" || status.ReservationStatus != "confirmed" {
		t.Fatal(status, e)
	}
}
func TestMoneyCents(t *testing.T) {
	for _, v := range []json.Number{"NaN", "-1", "1.001", "1e2", ""} {
		if _, e := moneyCents(v); e == nil {
			t.Fatal(v)
		}
	}
	for _, v := range []json.Number{"5000", "5000.0", "5000.00", "5000.000"} {
		n, e := moneyCents(v)
		if e != nil || n != 500000 {
			t.Fatal(v, n, e)
		}
	}
}
