package payments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
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
	err := s.DB.WithContext(ctx).First(&r, c.ReservationID).Error
	v := View{Checkout: c, ReservationStatus: string(r.Status), Verified: verified}
	if c.State == "pending" || c.State == "failed" {
		if r.Status == "pending" && s.Now().Before(c.ExpiresAt) {
			v.CheckoutURL = c.URL
		}
	}
	if r.Status == "pending" && !s.Now().Before(c.ExpiresAt) && c.State != "completed" && c.State != "review" {
		v.State = "expired"
	}
	return v, err
}
func (s *Service) Start(ctx context.Context, id uint, token string) (View, error) {
	if !s.Config.Ready() {
		return View{}, ErrUnavailable
	}
	if _, err := s.Bookings.Get(ctx, id, token); err != nil {
		return View{}, err
	}
	var c Checkout
	created := false
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Same writer lock used by cancel and confirmation; serialize checkouts across API instances.
		if err := tx.Model(&reservation.Reservation{}).Where("id = ?", id).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		var r reservation.Reservation
		if err := tx.First(&r, id).Error; err != nil {
			return err
		}
		if r.Status != "pending" || r.ExpiresAt == nil || !s.Now().Before(*r.ExpiresAt) {
			return ErrConflict
		}
		err := tx.Where("reservation_id = ?", id).First(&c).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		amount, err := PriceCents(r.DurationMinutes)
		if err != nil {
			return err
		}
		key := make([]byte, 24)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		c = Checkout{ReservationID: id, Reference: hex.EncodeToString(key), AmountCents: amount, Currency: "ARS", State: "creating", ExpiresAt: *r.ExpiresAt}
		if err = tx.Create(&c).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return View{}, err
	}
	if created {
		p, err := s.Gateway.CreatePreference(ctx, c)
		if err != nil {
			return View{}, ErrUnavailable
		} // Keep durable 'creating': never blindly repeat a potentially successful POST.
		checkoutURL := p.URL
		if !s.Config.LiveMode && p.SandboxURL != "" {
			checkoutURL = p.SandboxURL
		}
		u, e := url.Parse(checkoutURL)
		if e != nil || u.Scheme != "https" || !(u.Hostname() == "www.mercadopago.com.ar" || strings.HasSuffix(u.Hostname(), ".mercadopago.com.ar")) || p.ID == "" {
			return View{}, ErrUnavailable
		}
		c.PreferenceID = p.ID
		c.URL = checkoutURL
		c.State = "pending"
		if err = s.DB.WithContext(ctx).Model(&Checkout{}).Where("id = ?", c.ID).Updates(map[string]any{"preference_id": p.ID, "url": checkoutURL, "state": "pending"}).Error; err != nil {
			return View{}, err
		}
	}
	if c.State == "creating" {
		return View{}, ErrUnavailable
	}
	return s.view(ctx, c, false)
}

func (s *Service) Status(ctx context.Context, id uint, token string) (View, error) {
	if _, err := s.Bookings.Get(ctx, id, token); err != nil {
		return View{}, err
	}
	var c Checkout
	if err := s.DB.WithContext(ctx).Where("reservation_id = ?", id).First(&c).Error; err != nil {
		return View{}, err
	}
	if err := s.Reconcile(ctx, c); err != nil {
		return View{}, err
	}
	if err := s.DB.WithContext(ctx).First(&c, c.ID).Error; err != nil {
		return View{}, err
	}
	return s.view(ctx, c, true)
}
