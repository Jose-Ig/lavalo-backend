package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("checkout temporarily unavailable")
var ErrConflict = errors.New("reservation cannot be paid")
var ErrMismatch = errors.New("provider payment does not match checkout")

type Config struct {
	Enabled       bool
	LiveMode      bool
	AccessToken   string
	WebhookSecret string
	CollectorID   int64
	PublicURL     string
}

func ConfigFromEnv() Config {
	id, _ := strconv.ParseInt(os.Getenv("MP_COLLECTOR_ID"), 10, 64)
	return Config{Enabled: os.Getenv("MP_ENABLED") == "true", LiveMode: os.Getenv("MP_LIVE_MODE") == "true", AccessToken: os.Getenv("MP_ACCESS_TOKEN"), WebhookSecret: os.Getenv("MP_WEBHOOK_SECRET"), CollectorID: id, PublicURL: strings.TrimRight(os.Getenv("MP_PUBLIC_BASE_URL"), "/")}
}
func (c Config) Ready() bool {
	u, err := url.Parse(c.PublicURL)
	return c.Enabled && c.AccessToken != "" && c.WebhookSecret != "" && c.CollectorID > 0 && err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

type Preference struct {
	ID         string `json:"id"`
	URL        string `json:"init_point"`
	SandboxURL string `json:"sandbox_init_point"`
}
type ProviderPayment struct {
	ID          int64       `json:"id"`
	Reference   string      `json:"external_reference"`
	Amount      json.Number `json:"transaction_amount"`
	Refunded    json.Number `json:"transaction_amount_refunded"`
	Currency    string      `json:"currency_id"`
	CollectorID int64       `json:"collector_id"`
	LiveMode    bool        `json:"live_mode"`
	Status      string      `json:"status"`
	Detail      string      `json:"status_detail"`
	UpdatedAt   time.Time   `json:"date_last_updated"`
}
type Gateway interface {
	CreatePreference(context.Context, Checkout) (Preference, error)
	GetPayment(context.Context, string) (ProviderPayment, error)
	SearchPayments(context.Context, string) ([]ProviderPayment, error)
}
type MercadoPago struct {
	Config Config
	HTTP   *http.Client
}

func NewGateway(c Config) *MercadoPago {
	return &MercadoPago{Config: c, HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// API errors deliberately omit response bodies: they can contain buyer data or secrets.
func (m *MercadoPago) request(ctx context.Context, method, path string, body any, out any) error {
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.mercadopago.com"+path, &payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.Config.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUnavailable
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
		return ErrUnavailable
	}
	return nil
}
func (m *MercadoPago) CreatePreference(ctx context.Context, c Checkout) (Preference, error) {
	ret := m.Config.PublicURL + "/api/v1/payments/return"
	amount := json.Number(fmt.Sprintf("%d.%02d", c.AmountCents/100, c.AmountCents%100))
	body := map[string]any{
		"items":              []any{map[string]any{"id": strconv.FormatUint(uint64(c.ReservationID), 10), "title": "Reserva Uash", "quantity": 1, "unit_price": amount, "currency_id": c.Currency}},
		"external_reference": c.Reference,
		"back_urls":          map[string]string{"success": ret, "pending": ret, "failure": ret}, "auto_return": "approved",
		"notification_url": m.Config.PublicURL + "/api/v1/payments/webhook",
		"expires":          true, "expiration_date_to": c.ExpiresAt.Format(time.RFC3339),
		// Offline methods can settle after the ten-minute slot hold. Keep this release synchronous.
		"payment_methods": map[string]any{"excluded_payment_types": []any{map[string]string{"id": "ticket"}, map[string]string{"id": "atm"}}},
	}
	var p Preference
	err := m.request(ctx, http.MethodPost, "/checkout/preferences", body, &p)
	return p, err
}
func (m *MercadoPago) GetPayment(ctx context.Context, id string) (ProviderPayment, error) {
	var p ProviderPayment
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return p, ErrMismatch
	}
	err := m.request(ctx, http.MethodGet, "/v1/payments/"+id, nil, &p)
	return p, err
}
func (m *MercadoPago) SearchPayments(ctx context.Context, ref string) ([]ProviderPayment, error) {
	var all []ProviderPayment
	for offset := 0; offset < 1000; offset += 100 {
		var page struct {
			Results []ProviderPayment `json:"results"`
			Paging  struct {
				Total int `json:"total"`
			} `json:"paging"`
		}
		path := "/v1/payments/search?external_reference=" + url.QueryEscape(ref) + "&limit=100&offset=" + strconv.Itoa(offset)
		if err := m.request(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		if offset+len(page.Results) >= page.Paging.Total {
			return all, nil
		}
		if len(page.Results) == 0 {
			return nil, ErrUnavailable
		}
	}
	return nil, ErrUnavailable
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
