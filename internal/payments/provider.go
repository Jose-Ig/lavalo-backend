package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrUnavailable = errors.New("checkout temporarily unavailable")
var ErrProviderAuthorization = fmt.Errorf("Mercado Pago rejected API authorization: %w", ErrUnavailable)
var ErrConflict = errors.New("reservation cannot be paid")
var ErrMismatch = errors.New("provider order does not match checkout")

type Config struct {
	StatementDescriptor string
	Enabled             bool
	LiveMode            bool
	AccessToken         string
	WebhookSecret       string
	CollectorID         int64
	PublicURL           string
}

func ConfigFromEnv() Config {
	id, _ := strconv.ParseInt(os.Getenv("MP_COLLECTOR_ID"), 10, 64)
	return Config{StatementDescriptor: strings.TrimSpace(os.Getenv("MP_STATEMENT_DESCRIPTOR")), Enabled: os.Getenv("MP_ENABLED") == "true", LiveMode: os.Getenv("MP_LIVE_MODE") == "true", AccessToken: os.Getenv("MP_ACCESS_TOKEN"), WebhookSecret: os.Getenv("MP_WEBHOOK_SECRET"), CollectorID: id, PublicURL: strings.TrimRight(os.Getenv("MP_PUBLIC_BASE_URL"), "/")}
}
func (c Config) descriptor() string {
	if strings.TrimSpace(c.StatementDescriptor) == "" {
		return "UASH"
	}
	return strings.TrimSpace(c.StatementDescriptor)
}
func (c Config) Ready() bool {
	u, e := url.Parse(c.PublicURL)
	return utf8.RuneCountInString(c.descriptor()) <= 13 && c.Enabled && c.AccessToken != "" && c.WebhookSecret != "" && c.CollectorID > 0 && e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

// Order follows Checkout Pro Orders, not the Payments or merchant_orders API.
// Orders does not always return live_mode; the documented ORDTST ID identifies test orders.
type Order struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	ProcessingMode string    `json:"processing_mode"`
	Reference      string    `json:"external_reference"`
	Amount         string    `json:"total_amount"`
	PaidAmount     string    `json:"total_paid_amount"`
	Currency       string    `json:"currency"`
	SellerID       string    `json:"user_id"`
	LiveMode       *bool     `json:"live_mode,omitempty"`
	Status         string    `json:"status"`
	Detail         string    `json:"status_detail"`
	UpdatedAt      time.Time `json:"last_updated_date"`
	URL            string    `json:"checkout_url"`
	Transactions   struct {
		Payments []struct {
			Status string `json:"status"`
			Detail string `json:"status_detail"`
		} `json:"payments"`
	} `json:"transactions"`
}
type Gateway interface {
	CreateOrder(context.Context, Checkout) (Order, error)
	GetOrder(context.Context, string) (Order, error)
}
type MercadoPago struct {
	Config Config
	HTTP   *http.Client
}

func NewGateway(c Config) *MercadoPago {
	return &MercadoPago{Config: c, HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// No provider response bodies in errors: these may contain private buyer information.
func (m *MercadoPago) request(ctx context.Context, method, path string, body []byte, key string, out any) error {
	req, e := http.NewRequestWithContext(ctx, method, "https://api.mercadopago.com"+path, bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+m.Config.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Idempotency-Key", key)
	}
	resp, e := m.HTTP.Do(req)
	if e != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Never expose provider bodies, tokens or customer data. Only log an allowlisted policy code.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			var detail struct {
				Code string `json:"code"`
			}
			_ = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&detail)
			policy := "authorization_rejected"
			if detail.Code == "PA_UNAUTHORIZED_RESULT_FROM_POLICIES" {
				policy = detail.Code
			}
			log.Printf("Mercado Pago authorization rejected: http_status=%d code=%s", resp.StatusCode, policy)
			return ErrProviderAuthorization
		}
		return ErrUnavailable
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); e != nil {
		return ErrUnavailable
	}
	return nil
}
func (m *MercadoPago) CreateOrder(ctx context.Context, c Checkout) (Order, error) {
	var o Order
	if c.API != "orders" || c.IdempotencyKey == "" || !json.Valid([]byte(c.RequestBody)) {
		return o, ErrMismatch
	}
	e := m.request(ctx, http.MethodPost, "/v1/orders", []byte(c.RequestBody), c.IdempotencyKey, &o)
	return o, e
}
func (m *MercadoPago) GetOrder(ctx context.Context, id string) (Order, error) {
	var o Order
	if !validOrderID(id) {
		return o, ErrMismatch
	}
	e := m.request(ctx, http.MethodGet, "/v1/orders/"+strings.ToUpper(id), nil, "", &o)
	return o, e
}
func validOrderID(id string) bool {
	id = strings.ToUpper(id)
	if !strings.HasPrefix(id, "ORD") || len(id) < 6 || len(id) > 100 {
		return false
	}
	for _, c := range id {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func orderRequest(c Checkout, cfg Config, now time.Time, buyer reservation.Reservation) (string, error) {
	// Persist the exact bytes once. Relative expiry MUST NOT change when replaying an idempotent request.
	seconds := int64(c.ExpiresAt.Sub(now) / time.Second)
	if seconds <= 0 {
		return "", ErrConflict
	}
	amount := fmt.Sprintf("%d.%02d", c.AmountCents/100, c.AmountCents%100)
	ret := cfg.PublicURL + "/api/v1/payments/return"
	body := map[string]any{
		"type": "online", "processing_mode": "manual", "capture_mode": "automatic",
		"total_amount": amount, "external_reference": c.Reference, "expiration_time": fmt.Sprintf("PT%dS", seconds),
		"items": []any{map[string]any{"title": "Reserva Uash", "quantity": 1, "unit_price": amount}},
		"config": map[string]any{
			"statement_descriptor": cfg.descriptor(),
			"online":               map[string]any{"success_url": ret, "failure_url": ret, "pending_url": ret, "auto_return": "approved", "retries": map[string]bool{"allowed": true}},
			"payment_method":       map[string]any{"not_allowed_types": []string{"ticket", "atm"}},
		},
	}

	// Orders requires email whenever payer is present. Keep historical guest
	// reservations payable without manufacturing missing contact or identity data.
	if email := strings.TrimSpace(buyer.Email); email != "" {
		payer := map[string]any{"email": email}
		if buyer.FirstName != "" {
			payer["first_name"] = buyer.FirstName
		}
		if buyer.LastName != "" {
			payer["last_name"] = buyer.LastName
		}
		if buyer.PhoneAreaCode != "" && strings.HasPrefix(buyer.Phone, "+54"+buyer.PhoneAreaCode) {
			payer["phone"] = map[string]string{"area_code": buyer.PhoneAreaCode, "number": strings.TrimPrefix(buyer.Phone, "+54"+buyer.PhoneAreaCode)}
		}
		body["payer"] = payer
	}
	b, e := json.Marshal(body)
	return string(b), e
}
func validateOrder(o Order, c Checkout, cfg Config) error {
	amount, e := moneyCents(json.Number(o.Amount))
	if e != nil || !validOrderID(o.ID) || o.Type != "online" || o.ProcessingMode != "manual" || o.Reference != c.Reference || amount != c.AmountCents || o.Currency != c.Currency || o.SellerID != strconv.FormatInt(cfg.CollectorID, 10) || c.SellerID != o.SellerID || c.LiveMode != cfg.LiveMode || o.UpdatedAt.IsZero() {
		return ErrMismatch
	}
	isTest := strings.HasPrefix(strings.ToUpper(o.ID), "ORDTST")
	if isTest == cfg.LiveMode || (o.LiveMode != nil && *o.LiveMode != cfg.LiveMode) {
		return ErrMismatch
	}
	return nil
}

// moneyCents parses decimal JSON without binary floating point or rounding.
func moneyCents(n json.Number) (int64, error) {
	s := n.String()
	parts := strings.Split(s, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return 0, ErrMismatch
	}
	frac := "00"
	if len(parts) == 2 {
		frac = parts[1]
		if len(frac) > 2 {
			if strings.Trim(frac[2:], "0") != "" {
				return 0, ErrMismatch
			}
			frac = frac[:2]
		}
		frac += (strings.Repeat("0", 2-len(frac)))
	}
	for _, r := range parts[0] + frac {
		if r < '0' || r > '9' {
			return 0, ErrMismatch
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > 90000000000000 {
		return 0, ErrMismatch
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, ErrMismatch
	}
	return whole*100 + f, nil
}
