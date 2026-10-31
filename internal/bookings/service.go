// Package bookings implements guest reservations. Access tokens are capabilities:
// callers must retain them privately to retrieve or cancel their reservation.
package bookings

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"gorm.io/gorm"
)

var ErrConflict = errors.New("slot unavailable")
var ErrInvalid = errors.New("invalid reservation")

type Input struct {
	WhatsAppOptIn bool      `json:"whatsapp_opt_in"`
	Email         string    `json:"email"`
	SlotID        uint      `json:"slot_id"`
	StartTime     time.Time `json:"start_time"`
	Duration      int       `json:"duration_minutes"`
	Name          string    `json:"name"`
	Phone         string    `json:"phone"`
	Plate         string    `json:"vehicle_plate"`
	Model         string    `json:"vehicle_model"`
}

type Service struct {
	DB  *gorm.DB
	Now func() time.Time
}

func New(db *gorm.DB) *Service { return &Service{DB: db, Now: time.Now} }

var Location = func() *time.Location {
	l, err := time.LoadLocation("America/Argentina/Cordoba")
	if err != nil {
		panic(err)
	}
	return l
}()

func (s *Service) Validate(i Input) error {
	if email := strings.TrimSpace(i.Email); email != "" {
		address, err := mail.ParseAddress(email)
		if err != nil || len(email) > 254 || address.Address != email {
			return ErrInvalid
		}
	}

	start := i.StartTime.In(Location)
	now := s.Now().In(Location)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, Location)
	if i.SlotID == 0 || (i.Duration != 15 && i.Duration != 30 && i.Duration != 60) || !start.After(now) || !start.Before(midnight.AddDate(0, 0, 8)) || start.Second() != 0 || start.Nanosecond() != 0 || start.Minute()%15 != 0 || start.Hour() < 9 || start.Add(time.Duration(i.Duration)*time.Minute).After(time.Date(start.Year(), start.Month(), start.Day(), 21, 0, 0, 0, Location)) {
		return ErrInvalid
	}
	if len(strings.TrimSpace(i.Name)) < 2 || len(i.Name) > 100 || len(i.Phone) < 8 || len(i.Phone) > 25 {
		return ErrInvalid
	}
	for _, c := range i.Phone {
		if (c < '0' || c > '9') && c != '+' {
			return ErrInvalid
		}
	}
	return nil
}

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (s *Service) busy(db *gorm.DB, id uint, start, end time.Time) (bool, error) {
	start, end = start.UTC(), end.UTC()
	var count int64
	err := db.Model(&reservation.Reservation{}).Where("slot_id = ? AND start_time < ? AND end_time > ?", id, end, start).Where("status = ? OR (status = ? AND (expires_at IS NULL OR expires_at > ?))", "confirmed", "pending", s.Now()).Count(&count).Error
	return count > 0, err
}
func (s *Service) Create(ctx context.Context, i Input) (*reservation.Reservation, string, error) {
	if err := s.Validate(i); err != nil {
		return nil, "", err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	token := hex.EncodeToString(b)
	end := i.StartTime.Add(time.Duration(i.Duration) * time.Minute)
	expiry := s.Now().Add(10 * time.Minute)
	if expiry.After(i.StartTime) {
		expiry = i.StartTime
	}
	r := &reservation.Reservation{SlotID: i.SlotID, StartTime: i.StartTime.UTC(), EndTime: end.UTC(), DurationMinutes: i.Duration, Name: strings.TrimSpace(i.Name), Email: strings.TrimSpace(i.Email), WhatsAppOptIn: i.WhatsAppOptIn, Phone: i.Phone, VehiclePlate: strings.ToUpper(strings.TrimSpace(i.Plate)), VehicleModel: strings.TrimSpace(i.Model), AccessTokenHash: tokenHash(token), ExpiresAt: &expiry, Status: reservation.ReservationStatusPending}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// UPDATE takes a row lock in PostgreSQL and a writer lock in SQLite before checking conflicts.
		result := tx.Model(&slot.Slot{}).Where("id = ? AND is_available = ?", i.SlotID, true).Update("updated_at", s.Now())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConflict
		}
		busy, err := s.busy(tx, i.SlotID, r.StartTime, r.EndTime)
		if err != nil {
			return err
		}
		if busy {
			return ErrConflict
		}
		return tx.Create(r).Error
	})
	return r, token, err
}
func (s *Service) Get(ctx context.Context, id uint, token string) (*reservation.Reservation, error) {
	if len(token) != 64 {
		return nil, gorm.ErrRecordNotFound
	}
	var r reservation.Reservation
	err := s.DB.WithContext(ctx).Where("id = ? AND access_token_hash = ?", id, tokenHash(token)).First(&r).Error
	return &r, err
}
func (s *Service) Cancel(ctx context.Context, id uint, token string) error {
	r, err := s.Get(ctx, id, token)
	if err != nil {
		return err
	}
	if r.Status == reservation.ReservationStatusCancelled {
		return nil
	}
	// Paid cancellation requires a refund policy; do not silently cancel confirmed bookings.
	result := s.DB.WithContext(ctx).Model(r).Where("status = ?", "pending").Update("status", "cancelled")
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Service) Available(ctx context.Context, start time.Time, duration int) ([]slot.Slot, error) {
	if err := s.Validate(Input{SlotID: 1, StartTime: start, Duration: duration, Name: "Guest", Phone: "1234567890", Plate: "ABC123", Model: "Car"}); err != nil {
		return nil, err
	}
	var slots []slot.Slot
	db := s.DB.WithContext(ctx)
	if err := db.Where("is_available = ?", true).Order("id").Find(&slots).Error; err != nil {
		return nil, err
	}
	available := make([]slot.Slot, 0, len(slots))
	for _, sl := range slots {
		busy, err := s.busy(db, sl.ID, start, start.Add(time.Duration(duration)*time.Minute))
		if err != nil {
			return nil, fmt.Errorf("availability: %w", err)
		}
		if !busy {
			available = append(available, sl)
		}
	}
	return available, nil
}
