package bookings

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"gorm.io/gorm"
	"time"
)

type ExtensionOption struct {
	Minutes     int   `json:"minutes"`
	AmountCents int64 `json:"amount_cents"`
}

func (s *Service) extensionAllowed(r reservation.Reservation) bool {
	return r.ParentReservationID == nil && r.Status == "confirmed" && r.QRVerifiedAt != nil && !s.Now().Before(r.StartTime) && s.Now().Before(r.EndTime)
}
func (s *Service) extensionFree(tx *gorm.DB, r reservation.Reservation, minutes int) (bool, error) {
	if minutes != 3 && minutes != 15 && minutes != 30 && minutes != 60 {
		return false, ErrInvalid
	}
	end := r.EndTime.Add(time.Duration(minutes) * time.Minute)
	local := r.StartTime.In(Location)
	if end.After(time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 0, 0, Location)) {
		return false, nil
	}
	var sl slot.Slot
	if err := tx.First(&sl, r.SlotID).Error; err != nil {
		return false, err
	}
	if !sl.IsAvailable {
		return false, nil
	}
	busy, err := s.busy(tx, r.SlotID, r.EndTime, end)
	return !busy, err
}
func (s *Service) ExtensionOptions(ctx context.Context, code, date string) ([]ExtensionOption, error) {
	day, err := s.lookupDay([]string{date})
	if err != nil {
		return nil, err
	}
	tx := s.DB.WithContext(ctx)
	r, err := s.findByCode(tx, code, day)
	if err != nil {
		return nil, err
	}
	if !s.extensionAllowed(r) {
		return nil, ErrConflict
	}
	result := []ExtensionOption{}
	for _, minutes := range []int{3, 15, 30, 60} {
		free, err := s.extensionFree(tx, r, minutes)
		if err != nil {
			return nil, err
		}
		if free {
			amount, err := PriceCents(minutes)
			if err != nil {
				return nil, err
			}
			result = append(result, ExtensionOption{minutes, amount})
		}
	}
	return result, nil
}
func (s *Service) CreateExtension(ctx context.Context, code, date string, minutes int) (*reservation.Reservation, string, error) {
	day, err := s.lookupDay([]string{date})
	if err != nil {
		return nil, "", err
	}
	db := s.DB.WithContext(ctx)
	original, err := s.findByCode(db, code, day)
	if err != nil {
		return nil, "", err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return nil, "", err
	}
	token := hex.EncodeToString(secret)
	var child reservation.Reservation
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&slot.Slot{}).Where("id = ?", original.SlotID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		if err := tx.Model(&reservation.Reservation{}).Where("id = ?", original.ID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		r, err := s.findByCode(tx, code, day)
		if err != nil {
			return err
		}
		if r.SlotID != original.SlotID || !s.extensionAllowed(r) {
			return ErrConflict
		}
		free, err := s.extensionFree(tx, r, minutes)
		if err != nil {
			return err
		}
		if !free {
			return ErrConflict
		}
		expiry := s.Now().Add(10 * time.Minute)
		if expiry.After(r.EndTime) {
			expiry = r.EndTime
		}
		child = reservation.Reservation{ParentReservationID: &r.ID, SlotID: r.SlotID, StartTime: r.EndTime, EndTime: r.EndTime.Add(time.Duration(minutes) * time.Minute), DurationMinutes: minutes, Name: r.Name, FirstName: r.FirstName, LastName: r.LastName, PhoneAreaCode: r.PhoneAreaCode, Phone: r.Phone, Email: r.Email, AccessTokenHash: tokenHash(token), ExpiresAt: &expiry, Status: "pending"}
		return tx.Create(&child).Error
	})
	return &child, token, err
}
