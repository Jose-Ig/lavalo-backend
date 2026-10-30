package payments

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"net/http/httptest"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPreferenceRequestContract(t *testing.T) {
	cfg := Config{AccessToken: "private", PublicURL: "https://api.example.com"}
	gateway := NewGateway(cfg)
	gateway.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/checkout/preferences" || r.Header.Get("Authorization") != "Bearer private" {
			t.Fatal("invalid request")
		}
		var body struct {
			Items []struct {
				UnitPrice json.Number `json:"unit_price"`
				Currency  string      `json:"currency_id"`
			} `json:"items"`
			Reference    string `json:"external_reference"`
			Expires      bool   `json:"expires"`
			To           string `json:"expiration_date_to"`
			Notification string `json:"notification_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) != 1 || body.Items[0].UnitPrice != "1250.00" || body.Items[0].Currency != "ARS" || body.Reference != "reference" || !body.Expires || body.Notification != "https://api.example.com/api/v1/payments/webhook" || body.To == "" {
			t.Fatalf("bad preference: %+v", body)
		}
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(`{"id":"preference","init_point":"https://www.mercadopago.com.ar/checkout/test"}`)), Header: make(http.Header)}, nil
	})
	if _, err := gateway.CreatePreference(context.Background(), Checkout{ReservationID: 1, AmountCents: 125000, Currency: "ARS", Reference: "reference", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
}
func TestPaymentRoutesRejectUnsignedAndNeverTrustReturnParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, f, id, token := fixture(t)
	ctx := context.Background()
	v, err := s.Start(ctx, id, token)
	if err != nil {
		t.Fatal(err)
	}
	f.payments = []ProviderPayment{approved(s, v.Checkout)}
	router := gin.New()
	Register(router.Group("/api/v1"), s)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/payments/webhook?data.id=99", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/payments/return?status=approved&access_token=attacker", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "attacker") {
		t.Fatal(w.Code, w.Body.String())
	}
	r, err := s.Bookings.Get(ctx, id, token)
	if err != nil || r.Status != "pending" {
		t.Fatal("return confirmed booking", err)
	}
}
func TestDisabledCheckoutDoesNotCallProvider(t *testing.T) {
	s, f, id, token := fixture(t)
	s.Config.Enabled = false
	if _, err := s.Start(context.Background(), id, token); err != ErrUnavailable {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatal("provider called while disabled")
	}
}
