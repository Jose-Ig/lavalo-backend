package bookings

import (
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"gorm.io/gorm"
	"time"
)

// All allocators lock spaces in ID order before checking overlaps or moving a booking.
// The writes serialize PostgreSQL rows and SQLite writers; no availability result is trusted at save time.
func (s *Service) lockAssignmentSlots(tx *gorm.DB) ([]slot.Slot, error) {
	var all []slot.Slot
	if err := tx.Order("id").Find(&all).Error; err != nil {
		return nil, err
	}
	for _, sl := range all {
		result := tx.Model(&slot.Slot{}).Where("id = ?", sl.ID).Update("updated_at", s.Now())
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, ErrConflict
		}
	}
	var enabled []slot.Slot
	if err := tx.Where("is_available = ?", true).Order("id").Find(&enabled).Error; err != nil {
		return nil, err
	}
	return enabled, nil
}
func (s *Service) chooseAssignment(tx *gorm.DB, r reservation.Reservation, candidates []slot.Slot, start time.Time) (uint, error) {
	// When editing, retain the current space if it is still available at the new time.
	for _, sl := range candidates {
		if sl.ID != r.SlotID {
			continue
		}
		busy, err := s.rescheduleBusy(tx, r, sl.ID, start)
		if err != nil {
			return 0, err
		}
		if !busy {
			return sl.ID, nil
		}
	}
	for _, sl := range candidates {
		if sl.ID == r.SlotID {
			continue
		}
		busy, err := s.rescheduleBusy(tx, r, sl.ID, start)
		if err != nil {
			return 0, err
		}
		if !busy {
			return sl.ID, nil
		}
	}
	return 0, ErrConflict
}
