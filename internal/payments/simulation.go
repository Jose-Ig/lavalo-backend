package payments

import (
	"context"
	"errors"
	"fmt"
	"github.com/Jose-Ig/lavalo-backend/internal/bookings"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Simulation never creates or changes a provider order and is unavailable in release/live mode.
func (s *Service) mockEnabled() bool {
	return s.Config.MockPayments && !s.Config.LiveMode && gin.Mode() != gin.ReleaseMode
}
func (s *Service) simulatePayment(ctx context.Context, id uint, token string) (View, error) {
	if !s.mockEnabled() {
		return View{}, ErrUnavailable
	}
	original, err := s.Bookings.Get(ctx, id, token)
	if err != nil {
		return View{}, err
	}
	var checkout Checkout
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&slot.Slot{}).Where("id = ?", original.SlotID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		if err := tx.Model(&reservation.Reservation{}).Where("id = ?", id).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		var r reservation.Reservation
		if err := tx.First(&r, id).Error; err != nil {
			return err
		}
		err := tx.Where("reservation_id = ?", id).First(&checkout).Error
		if err == nil {
			// Do not overwrite real attempts, including pending provider orders.
			if checkout.API != "mock" || checkout.State != "completed" || r.Status != "confirmed" {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if r.Status != "pending" || r.ExpiresAt == nil || !s.Now().Before(*r.ExpiresAt) {
			return ErrConflict
		}
		var overlaps int64
		if err := tx.Model(&reservation.Reservation{}).Where("id <> ? AND slot_id = ? AND start_time < ? AND end_time > ?", id, r.SlotID, r.EndTime, r.StartTime).Where("status = ? OR (status = ? AND (expires_at IS NULL OR expires_at > ?))", "confirmed", "pending", s.Now()).Count(&overlaps).Error; err != nil {
			return err
		}
		if overlaps > 0 {
			return ErrConflict
		}
		amount, err := PriceCents(r.DurationMinutes)
		if err != nil {
			return err
		}
		checkout = Checkout{API: "mock", ReservationID: id, Reference: fmt.Sprintf("local-simulation-%d", id), AmountCents: amount, Currency: "ARS", State: "completed", ExpiresAt: *r.ExpiresAt}
		if err := tx.Create(&checkout).Error; err != nil {
			return err
		}
		r.Status = "confirmed"
		r.ExpiresAt = nil
		if err := tx.Model(&r).Updates(map[string]any{"status": r.Status, "expires_at": nil}).Error; err != nil {
			return err
		}
		return bookings.EnsureConfirmation(tx, &r)
	})
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, checkout, true)
}
