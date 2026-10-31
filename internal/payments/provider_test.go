package payments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOrderRequestContract(t *testing.T) {
	cfg := Config{AccessToken: "private", PublicURL: "https://api.example.com"}
	gateway := NewGateway(cfg)
	now := time.Now()
	c := Checkout{API: "orders", IdempotencyKey: "d3306682-f4de-4c93-9444-9151643b587a", ReservationID: 1, AmountCents: 125000, Currency: "ARS", Reference: "reference", ExpiresAt: now.Add(10 * time.Minute)}
	var err error
	c.RequestBody, err = orderRequest(c, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	gateway.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/v1/orders" || r.Header.Get("Authorization") != "Bearer private" || r.Header.Get("X-Idempotency-Key") != c.IdempotencyKey {
			t.Fatal("invalid Orders request")
		}
		var body struct {
			Type      string `json:"type"`
			Mode      string `json:"processing_mode"`
			Capture   string `json:"capture_mode"`
			Total     string `json:"total_amount"`
			Expiry    string `json:"expiration_time"`
			Reference string `json:"external_reference"`
			Items     []struct {
				Price    string `json:"unit_price"`
				Quantity int    `json:"quantity"`
			} `json:"items"`
			Config struct {
				Online struct {
					Success string `json:"success_url"`
					Failure string `json:"failure_url"`
					Pending string `json:"pending_url"`
					Auto    string `json:"auto_return"`
				} `json:"online"`
				Method struct {
					Excluded []string `json:"not_allowed_types"`
				} `json:"payment_method"`
			} `json:"config"`
		}
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			t.Fatal(e)
		}
		if string(raw) != c.RequestBody {
			t.Fatal("stored body mutated")
		}
		if e = json.Unmarshal(raw, &body); e != nil {
			t.Fatal(e)
		}
		if body.Type != "online" || body.Mode != "manual" || body.Capture != "automatic" || body.Total != "1250.00" || body.Expiry != "PT600S" || body.Reference != "reference" || len(body.Items) != 1 || body.Items[0].Price != "1250.00" || body.Config.Online.Success != "https://api.example.com/api/v1/payments/return" || body.Config.Online.Pending != body.Config.Online.Success || body.Config.Online.Failure != body.Config.Online.Success || body.Config.Online.Auto != "approved" || len(body.Config.Method.Excluded) != 2 {
			t.Fatalf("bad order: %+v", body)
		}
		for _, legacy := range []string{"back_urls", "notification_url", "init_point", "expiration_date_to"} {
			if strings.Contains(string(raw), legacy) {
				t.Fatal("legacy preference field", legacy)
			}
		}
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(`{"id":"ORDTST01ABC","checkout_url":"https://www.mercadopago.com.ar/checkout/v1/redirect?order_id=ORDTST01ABC"}`)), Header: make(http.Header)}, nil
	})
	if _, err = gateway.CreateOrder(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}
func TestOrderLookupUsesOrdersEndpoint(t *testing.T) {
	gateway := NewGateway(Config{AccessToken: "private"})
	gateway.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/v1/orders/ORDTST01ABC" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"ORDTST01ABC","total_amount":"5000.00","total_paid_amount":"5000.00","user_id":"123","currency":"ARS","status":"processed","status_detail":"accredited"}`))}, nil
	})
	o, e := gateway.GetOrder(context.Background(), "ORDTST01ABC")
	if e != nil || o.SellerID != "123" || o.PaidAmount != "5000.00" {
		t.Fatal(o, e)
	}
	if _, e = gateway.GetOrder(context.Background(), "../../users"); e == nil {
		t.Fatal("invalid order ID accepted")
	}
}
func TestOrderWebhookAndReturn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, f, id, token := fixture(t)
	ctx := context.Background()
	v, e := s.Start(ctx, id, token)
	if e != nil {
		t.Fatal(e)
	}
	f.set(approved(s, v.Checkout))
	router := gin.New()
	Register(router.Group("/api/v1"), s)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/payments/webhook?data.id="+*v.OrderID, nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/payments/return?status=approved&access_token=attacker", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "attacker") {
		t.Fatal(w.Code, w.Body.String())
	}
	r, e := s.Bookings.Get(ctx, id, token)
	if e != nil || r.Status != "pending" {
		t.Fatal("return confirmed booking", e)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, signedRequest(*v.OrderID))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	r, e = s.Bookings.Get(ctx, id, token)
	if e != nil || r.Status != "confirmed" {
		t.Fatal("signed order not confirmed", e)
	}
}
func TestDisabledCheckoutDoesNotCallProvider(t *testing.T) {
	s, f, id, token := fixture(t)
	s.Config.Enabled = false
	if _, e := s.Start(context.Background(), id, token); e != ErrUnavailable {
		t.Fatal(e)
	}
	if f.calls != 0 {
		t.Fatal("provider called while disabled")
	}
}

func TestProviderAuthorizationErrorIsDistinctAndDoesNotLeakDetails(t *testing.T) {
	for _, status := range []int{401, 403} {
		gateway := NewGateway(Config{AccessToken: "private-secret"})
		gateway.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"code":"PA_UNAUTHORIZED_RESULT_FROM_POLICIES","message":"private-secret buyer@example.com"}`))}, nil
		})
		_, err := gateway.GetOrder(context.Background(), "ORDTST01ABC")
		if !errors.Is(err, ErrProviderAuthorization) || !errors.Is(err, ErrUnavailable) {
			t.Fatal("authorization error not classified", err)
		}
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.GET("/failure", func(c *gin.Context) { failure(c, err) })
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/failure", nil))
		if w.Code != 503 || !strings.Contains(w.Body.String(), "payment_provider_authorization") {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, secret := range []string{"private-secret", "buyer@example.com", "PA_UNAUTHORIZED_RESULT_FROM_POLICIES"} {
			if strings.Contains(err.Error(), secret) || strings.Contains(w.Body.String(), secret) {
				t.Fatal("private provider details exposed")
			}
		}
	}
}
