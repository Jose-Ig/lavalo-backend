package payments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Jose-Ig/lavalo-backend/internal/bookings"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"gorm.io/gorm"
)

type Service struct {
	DB       *gorm.DB
	Bookings *bookings.Service
	Gateway  Gateway
	Config   Config
	Now      func() time.Time
}

func New(db *gorm.DB, cfg Config) *Service {
	return &Service{DB: db, Bookings: bookings.New(db), Gateway: NewGateway(cfg), Config: cfg, Now: time.Now}
}

type View struct {
	Checkout
	CheckoutURL       string `json:"checkout_url,omitempty"`
	ReservationStatus string `json:"reservation_status"`
	Verified          bool   `json:"verified"`
}

func (s *Service) view(ctx context.Context, c Checkout, verified bool) (View, error) {
	var r reservation.Reservation
	e := s.DB.WithContext(ctx).First(&r, c.ReservationID).Error
	v := View{Checkout: c, ReservationStatus: string(r.Status), Verified: verified}
	if c.API != "orders" {
		v.State = "review"
		v.Issue = "legacy_preferences"
		v.Verified = false
		return v, e
	}
	if (c.State == "pending" || c.State == "failed") && c.Issue != "order_canceled" && r.Status == "pending" && s.Now().Before(c.ExpiresAt) {
		v.CheckoutURL = c.URL
	}
	if r.Status == "pending" && !s.Now().Before(c.ExpiresAt) && c.State != "completed" && c.State != "review" {
		v.State = "expired"
	}
	return v, e
}
func uuid() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func (s *Service) Start(ctx context.Context, id uint, token string) (View, error) {
	if !s.Config.Ready() {
		return View{}, ErrUnavailable
	}
	if _, e := s.Bookings.Get(ctx, id, token); e != nil {
		return View{}, e
	}
	var c Checkout
	e := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(&reservation.Reservation{}).Where("id = ?", id).Update("updated_at", s.Now()).Error; e != nil {
			return e
		}
		var r reservation.Reservation
		if e := tx.First(&r, id).Error; e != nil {
			return e
		}
		if r.Status != "pending" || r.ExpiresAt == nil || !s.Now().Before(*r.ExpiresAt) {
			return ErrConflict
		}
		e := tx.Where("reservation_id = ?", id).First(&c).Error
		if e == nil {
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		amount, e := PriceCents(r.DurationMinutes)
		if e != nil {
			return e
		}
		key := make([]byte, 24)
		if _, e = rand.Read(key); e != nil {
			return e
		}
		idempotency, e := uuid()
		if e != nil {
			return e
		}
		c = Checkout{API: "orders", SellerID: strconv.FormatInt(s.Config.CollectorID, 10), LiveMode: s.Config.LiveMode, IdempotencyKey: idempotency, ReservationID: id, Reference: hex.EncodeToString(key), AmountCents: amount, Currency: "ARS", State: "creating", ExpiresAt: *r.ExpiresAt}
		c.RequestBody, e = orderRequest(c, s.Config, s.Now())
		if e != nil {
			return e
		}
		return tx.Create(&c).Error
	})
	if e != nil {
		return View{}, e
	}
	if c.API != "orders" {
		return s.view(ctx, c, false)
	}
	if c.SellerID != strconv.FormatInt(s.Config.CollectorID, 10) || c.LiveMode != s.Config.LiveMode {
		return View{}, ErrMismatch
	}
	if c.OrderID == nil {
		if e = s.ensureOrder(ctx, &c); e != nil {
			return View{}, e
		}
	}
	return s.view(ctx, c, false)
}

// ensureOrder may replay a POST, always with the same UUID and stored request body.
// The reservation hold is <=10 minutes, limiting replay to the original short attempt window.
func (s *Service) ensureOrder(ctx context.Context, c *Checkout) error {
	if c.API != "orders" {
		return ErrConflict
	}
	if c.SellerID != strconv.FormatInt(s.Config.CollectorID, 10) || c.LiveMode != s.Config.LiveMode {
		return ErrMismatch
	}
	if c.OrderID != nil {
		return nil
	}
	if !s.Now().Before(c.ExpiresAt) {
		return nil
	}
	o, e := s.Gateway.CreateOrder(ctx, *c)
	if e != nil {
		return e
	}
	if e = validateOrder(o, *c, s.Config); e != nil {
		return e
	}
	u, e := url.Parse(o.URL)
	if e != nil || u.Scheme != "https" || u.User != nil || !strings.HasSuffix(u.Hostname(), ".mercadopago.com.ar") {
		return ErrMismatch
	}
	// Creation can return an existing processed order when recovering a lost response.
	// Never overwrite a confirmation/refund that a concurrent webhook has already stored.
	e = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(&Checkout{}).Where("id = ?", c.ID).Update("updated_at", s.Now()).Error; e != nil {
			return e
		}
		var latest Checkout
		if e := tx.First(&latest, c.ID).Error; e != nil {
			return e
		}
		if latest.OrderID != nil && *latest.OrderID != o.ID {
			return ErrMismatch
		}
		updates := map[string]any{"order_id": o.ID, "url": o.URL}
		if latest.State == "creating" {
			updates["state"] = "pending"
		}
		return tx.Model(&latest).Updates(updates).Error
	})
	if e != nil {
		return e
	}
	if e = s.DB.WithContext(ctx).First(c, c.ID).Error; e != nil {
		return e
	}
	if e = s.Apply(ctx, o); e != nil {
		return e
	}
	return s.DB.WithContext(ctx).First(c, c.ID).Error
}
func (s *Service) Status(ctx context.Context, id uint, token string) (View, error) {
	if _, e := s.Bookings.Get(ctx, id, token); e != nil {
		return View{}, e
	}
	var c Checkout
	if e := s.DB.WithContext(ctx).Where("reservation_id = ?", id).First(&c).Error; e != nil {
		return View{}, e
	}
	if c.API != "orders" {
		return s.view(ctx, c, false)
	}
	if e := s.Reconcile(ctx, c); e != nil {
		return View{}, e
	}
	if e := s.DB.WithContext(ctx).First(&c, c.ID).Error; e != nil {
		return View{}, e
	}
	return s.view(ctx, c, c.OrderID != nil)
}
