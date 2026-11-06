package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Jose-Ig/lavalo-backend/internal/controllers"
	"github.com/Jose-Ig/lavalo-backend/internal/operations"
	"net/http"
	"net/http/httptest"
	"strings"
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
	mu     sync.Mutex
	calls  int
	fail   bool
	keys   []string
	bodies []string
	orders map[string]Order
}

func (f *fakeGateway) CreateOrder(_ context.Context, c Checkout) (Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.keys = append(f.keys, c.IdempotencyKey)
	f.bodies = append(f.bodies, c.RequestBody)
	id := fmt.Sprintf("ORDTST%020d", c.ID)
	if f.orders == nil {
		f.orders = map[string]Order{}
	}
	// Simulate provider creation even when the response is lost.
	if _, exists := f.orders[id]; !exists {
		f.orders[id] = Order{ID: id, Type: "online", ProcessingMode: "manual", Reference: c.Reference, Amount: fmt.Sprintf("%d.%02d", c.AmountCents/100, c.AmountCents%100), PaidAmount: "0.00", Currency: "ARS", SellerID: "123", Status: "created", Detail: "created", UpdatedAt: c.ExpiresAt.Add(-10 * time.Minute), URL: "https://www.mercadopago.com.ar/checkout/v1/redirect?order_id=" + id}
	}
	if f.fail {
		return Order{}, ErrUnavailable
	}
	return f.orders[id], nil
}
func (f *fakeGateway) GetOrder(_ context.Context, id string) (Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return Order{}, ErrUnavailable
	}
	return o, nil
}
func (f *fakeGateway) set(o Order) { f.mu.Lock(); defer f.mu.Unlock(); f.orders[o.ID] = o }
func fixture(t *testing.T) (*Service, *fakeGateway, uint, string) {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	if e = db.AutoMigrate(&operations.Session{}, &operations.Execution{}, &operations.Incident{}, &operations.Boot{}, &controllers.Device{}, &reservation.Reservation{}, &slot.Slot{}, &Checkout{}, &Receipt{}, &bookings.NotificationIntent{}); e != nil {
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
func approved(s *Service, c Checkout) Order {
	return Order{ID: *c.OrderID, Type: "online", ProcessingMode: "manual", Reference: c.Reference, Amount: "5000.00", PaidAmount: "5000.00", Currency: "ARS", SellerID: "123", Status: "processed", Detail: "accredited", UpdatedAt: s.Now().Add(time.Second)}
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
	if len(f.orders) != 1 {
		t.Fatalf("created %d orders", len(f.orders))
	}
	for i, key := range f.keys {
		if key == "" || key != f.keys[0] || f.bodies[i] != f.bodies[0] {
			t.Fatal("retry changed key/payload")
		}
	}
	var c Checkout
	s.DB.First(&c)
	if c.AmountCents != 500000 || c.API != "orders" || c.OrderID == nil {
		t.Fatalf("invalid checkout: %+v", c)
	}
}
func TestLostCreationResponseReplaysSameOrderAfterRestart(t *testing.T) {
	s, f, id, token := fixture(t)
	ctx := context.Background()
	f.fail = true
	if _, e := s.Start(ctx, id, token); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	var c Checkout
	s.DB.First(&c)
	originalBody, key := c.RequestBody, c.IdempotencyKey
	f.fail = false
	restarted := New(s.DB, s.Config)
	restarted.Gateway = f
	restarted.Now = s.Now
	restarted.Bookings.Now = s.Now
	v, e := restarted.Start(ctx, id, token)
	if e != nil {
		t.Fatal(e)
	}
	if v.OrderID == nil || len(f.orders) != 1 || f.calls != 2 || key != f.keys[1] || originalBody != f.bodies[1] {
		t.Fatal("lost response duplicated order")
	}
}
func TestExpiredUnknownCreationIsNotReplayed(t *testing.T) {
	s, f, id, token := fixture(t)
	ctx := context.Background()
	f.fail = true
	s.Start(ctx, id, token)
	now := s.Now().Add(11 * time.Minute)
	s.Now = func() time.Time { return now }
	s.Bookings.Now = s.Now
	f.fail = false
	v, e := s.Status(ctx, id, token)
	if e != nil || v.State != "expired" || v.Verified || f.calls != 1 {
		t.Fatal(v, e, f.calls)
	}
}
func TestConfirmationReplayRefundAndStaleOrder(t *testing.T) {
	s, _, id, token := fixture(t)
	ctx := context.Background()
	v, e := s.Start(ctx, id, token)
	if e != nil {
		t.Fatal(e)
	}
	o := approved(s, v.Checkout)
	for i := 0; i < 2; i++ {
		if e = s.Apply(ctx, o); e != nil {
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
	refunded := o
	refunded.Status = "refunded"
	refunded.Detail = "refunded"
	refunded.UpdatedAt = refunded.UpdatedAt.Add(time.Second)
	if e = s.Apply(ctx, refunded); e != nil {
		t.Fatal(e)
	}
	if e = s.Apply(ctx, o); e != nil {
		t.Fatal(e)
	}
	r, _ = s.Bookings.Get(ctx, id, token)
	if r.Status != "cancelled" {
		t.Fatal(r.Status)
	}
}
func TestMismatchCannotConfirm(t *testing.T) {
	for _, field := range []string{"amount", "paid", "currency", "seller", "live", "mode", "reference", "order_id"} {
		t.Run(field, func(t *testing.T) {
			s, _, id, token := fixture(t)
			v, e := s.Start(context.Background(), id, token)
			if e != nil {
				t.Fatal(e)
			}
			o := approved(s, v.Checkout)
			switch field {
			case "amount":
				o.Amount = "0.01"
			case "paid":
				o.PaidAmount = "4999.99"
			case "currency":
				o.Currency = "USD"
			case "seller":
				o.SellerID = "999"
			case "live":
				o.ID = "ORD00000000000000000001"
			case "mode":
				o.ProcessingMode = "automatic"
			case "reference":
				o.Reference = "wrong"
			case "order_id":
				o.ID += "9"
			}
			if e = s.Apply(context.Background(), o); e == nil {
				t.Fatal("mismatch accepted")
			}
			r, _ := s.Bookings.Get(context.Background(), id, token)
			if r.Status != "pending" {
				t.Fatal(r.Status)
			}
		})
	}
}
func TestLateAndCancelledOrderNeedsReview(t *testing.T) {
	for _, scenario := range []string{"late", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, id, token := fixture(t)
			ctx := context.Background()
			v, e := s.Start(ctx, id, token)
			if e != nil {
				t.Fatal(e)
			}
			o := approved(s, v.Checkout)
			if scenario == "late" {
				now := s.Now().Add(11 * time.Minute)
				s.Now = func() time.Time { return now }
				s.Bookings.Now = s.Now
			} else {
				if e = s.Bookings.Cancel(ctx, id, token); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.Apply(ctx, o); e != nil {
				t.Fatal(e)
			}
			var c Checkout
			s.DB.First(&c, v.ID)
			if c.State != "review" {
				t.Fatal(c.State)
			}
			r, _ := s.Bookings.Get(ctx, id, token)
			if r.Status == "confirmed" {
				t.Fatal("late booking confirmed")
			}
		})
	}
}
func TestOnlyAccreditedOrdersConfirm(t *testing.T) {
	for _, scenario := range []struct{ status, detail, want string }{{"created", "created", "pending"}, {"processing", "in_process", "pending"}, {"action_required", "waiting_capture", "pending"}, {"failed", "high_risk", "failed"}, {"canceled", "canceled", "failed"}, {"processed", "partially_refunded", "review"}, {"processed", "unknown", "review"}, {"new_status", "unknown", "review"}} {
		t.Run(scenario.status+scenario.detail, func(t *testing.T) {
			s, _, id, token := fixture(t)
			ctx := context.Background()
			v, e := s.Start(ctx, id, token)
			if e != nil {
				t.Fatal(e)
			}
			o := approved(s, v.Checkout)
			o.Status = scenario.status
			o.Detail = scenario.detail
			if e = s.Apply(ctx, o); e != nil {
				t.Fatal(e)
			}
			var c Checkout
			s.DB.First(&c, v.ID)
			if c.State != scenario.want {
				t.Fatal(c.State)
			}
			r, _ := s.Bookings.Get(ctx, id, token)
			if r.Status == "confirmed" {
				t.Fatal("unaccredited order confirmed")
			}
		})
	}
}
func signedRequest(id string) *http.Request {
	req := httptest.NewRequest("POST", "/api/v1/payments/webhook?data.id="+id+"&type=order", nil)
	req.Header.Set("x-request-id", "request")
	mac := hmac.New(sha256.New, []byte("secret"))
	fmt.Fprintf(mac, "id:%s;request-id:request;ts:1704908010;", strings.ToLower(id))
	req.Header.Set("x-signature", "ts=1704908010,v1="+hex.EncodeToString(mac.Sum(nil)))
	return req
}
func TestSignedAlphanumericIDAndReconciliation(t *testing.T) {
	req := signedRequest("ORDTST01ABC")
	if !VerifySignature(req, "secret") {
		t.Fatal("valid signature rejected")
	}
	req.URL.RawQuery = "data.id=ORDTST01OTHER"
	if VerifySignature(req, "secret") {
		t.Fatal("unsigned id accepted")
	}
	if VerifySignature(signedRequest("99"), "secret") {
		t.Fatal("payment ID accepted as order")
	}
	s, f, id, token := fixture(t)
	v, e := s.Start(context.Background(), id, token)
	if e != nil {
		t.Fatal(e)
	}
	f.set(approved(s, v.Checkout))
	status, e := s.Status(context.Background(), id, token)
	if e != nil || !status.Verified || status.State != "completed" || status.ReservationStatus != "confirmed" {
		t.Fatal(status, e)
	}
}
func TestLegacyPreferenceNeverCreatesOrder(t *testing.T) {
	s, f, id, token := fixture(t)
	c := Checkout{ReservationID: id, Reference: "legacy", AmountCents: 500000, Currency: "ARS", PreferenceID: "old-pref", URL: "https://www.mercadopago.com.ar/old", State: "pending", ExpiresAt: s.Now().Add(time.Minute)}
	if e := s.DB.Create(&c).Error; e != nil {
		t.Fatal(e)
	}
	for _, start := range []bool{true, false} {
		var v View
		var e error
		if start {
			v, e = s.Start(context.Background(), id, token)
		} else {
			v, e = s.Status(context.Background(), id, token)
		}
		if e != nil || v.Issue != "legacy_preferences" || v.CheckoutURL != "" || v.Verified {
			t.Fatal(v, e)
		}
	}
	if f.calls != 0 {
		t.Fatal("legacy preference converted to second charge")
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

func TestEnvironmentChangeCannotReplayCreation(t *testing.T) {
	for _, change := range []string{"seller", "mode"} {
		t.Run(change, func(t *testing.T) {
			s, f, id, token := fixture(t)
			f.fail = true
			if _, e := s.Start(context.Background(), id, token); !errors.Is(e, ErrUnavailable) {
				t.Fatal(e)
			}
			f.fail = false
			if change == "seller" {
				s.Config.CollectorID = 999
			} else {
				s.Config.LiveMode = true
			}
			if _, e := s.Start(context.Background(), id, token); !errors.Is(e, ErrMismatch) {
				t.Fatal(e)
			}
			if f.calls != 1 {
				t.Fatal("replayed in a different account/environment")
			}
		})
	}
}
func TestPartialRefundCannotReconfirm(t *testing.T) {
	s, _, id, token := fixture(t)
	ctx := context.Background()
	v, e := s.Start(ctx, id, token)
	if e != nil {
		t.Fatal(e)
	}
	o := approved(s, v.Checkout)
	if e = s.Apply(ctx, o); e != nil {
		t.Fatal(e)
	}
	o.Detail = "partially_refunded"
	o.UpdatedAt = o.UpdatedAt.Add(time.Second)
	if e = s.Apply(ctx, o); e != nil {
		t.Fatal(e)
	}
	o.Detail = "accredited"
	o.UpdatedAt = o.UpdatedAt.Add(time.Second)
	if e = s.Apply(ctx, o); e != nil {
		t.Fatal(e)
	}
	var c Checkout
	s.DB.First(&c, v.ID)
	r, _ := s.Bookings.Get(ctx, id, token)
	if c.State != "review" || c.Issue != "partial_refund" || r.Status == "confirmed" {
		t.Fatal(c, r.Status)
	}
}
func TestOrdersMigrationPreservesLegacyRows(t *testing.T) {
	s, _, _, _ := fixture(t)
	if e := s.DB.Migrator().DropTable(&Checkout{}); e != nil {
		t.Fatal(e)
	}
	// Original Preferences schema: no order/environment/idempotency fields.
	schema := `CREATE TABLE checkouts (id integer primary key, reservation_id integer NOT NULL, reference text NOT NULL,
 amount_cents integer NOT NULL, currency text, preference_id text, url text, state text, issue text,
 expires_at datetime, created_at datetime, updated_at datetime, reconciled_at datetime)`
	if e := s.DB.Exec(schema).Error; e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 2; i++ {
		if e := s.DB.Exec(`INSERT INTO checkouts (id,reservation_id,reference,amount_cents,currency,preference_id,state,expires_at) VALUES (?,?,?,?,?,?,?,?)`, i, i, fmt.Sprintf("legacy%d", i), 500000, "ARS", fmt.Sprintf("preference%d", i), "pending", s.Now()).Error; e != nil {
			t.Fatal(e)
		}
	}
	if e := s.DB.AutoMigrate(&Checkout{}); e != nil {
		t.Fatal(e)
	}
	var rows []Checkout
	if e := s.DB.Order("id").Find(&rows).Error; e != nil {
		t.Fatal(e)
	}
	if len(rows) != 2 {
		t.Fatal(len(rows))
	}
	for i, c := range rows {
		if c.PreferenceID != fmt.Sprintf("preference%d", i+1) || c.OrderID != nil || c.API != "" || c.AmountCents != 500000 {
			t.Fatalf("migration altered legacy checkout: %+v", c)
		}
	}
}

func TestRemovedMockFlagCannotAutoApprovePayment(t *testing.T) {
	t.Setenv("MP_MOCK_PAYMENTS", "true")
	s, gateway, id, token := fixture(t)
	view, err := s.Start(context.Background(), id, token)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.calls != 1 || view.CheckoutURL == "" || view.State == "completed" || view.ReservationStatus != "pending" || view.ConfirmationCode != "" {
		t.Fatal("payment must go through provider without local approval", view)
	}
}
func TestHistoricalMockCheckoutIsNotVerifiedAsProviderPayment(t *testing.T) {
	s, _, id, token := fixture(t)
	checkout := Checkout{API: "mock", ReservationID: id, Reference: "historical-local-simulation", State: "completed", AmountCents: 500000, Currency: "ARS", ExpiresAt: s.Now().Add(time.Minute)}
	if err := s.DB.Create(&checkout).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Status(context.Background(), id, token); !errors.Is(err, ErrUnavailable) {
		t.Fatal("historical simulation accepted", err)
	}
}

func TestConfirmedPaymentSeparatesPublicIDAndSixDigitCode(t *testing.T) {
	s, f, id, token := fixture(t)
	ctx := context.Background()
	pending, err := s.Start(ctx, id, token)
	if err != nil {
		t.Fatal(err)
	}
	if pending.ReservationNumber != "" || pending.ConfirmationCode != "" {
		t.Fatal("unpaid code exposed")
	}
	var c Checkout
	s.DB.Where("reservation_id = ?", id).First(&c)
	order := approved(s, c)
	f.set(order)
	view, err := s.Status(ctx, id, token)
	if err != nil {
		t.Fatal(err)
	}
	var r reservation.Reservation
	s.DB.First(&r, id)
	code := bookings.ConfirmationCode(r)
	if len(code) != 6 || view.ConfirmationCode != code || view.ReservationNumber != code+"-"+r.StartTime.In(bookings.Location).Format("020106") {
		t.Fatal("incorrect ID or customer code", view.ReservationNumber, view.ConfirmationCode)
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Fatal("non-numeric code")
		}
	}
}
