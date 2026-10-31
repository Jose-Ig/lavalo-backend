package bookings

import (
	"context"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"gorm.io/gorm"
	"sort"
	"time"
)

func (s *Service) canReschedule(r reservation.Reservation) bool {
	return r.Status == "confirmed" && r.QRVerifiedAt == nil && s.Now().Before(r.StartTime)
}
func (s *Service) rescheduleInput(r reservation.Reservation, slotID uint, start time.Time) Input {
	return Input{SlotID: slotID, StartTime: start, Duration: r.DurationMinutes, Name: r.Name, Phone: r.Phone}
}
func (s *Service) rescheduleBusy(db *gorm.DB, r reservation.Reservation, slotID uint, start time.Time) (bool, error) {
	var count int64
	err := db.Model(&reservation.Reservation{}).Where("id <> ? AND slot_id = ? AND start_time < ? AND end_time > ?", r.ID, slotID, start.Add(time.Duration(r.DurationMinutes)*time.Minute).UTC(), start.UTC()).Where("status = ? OR (status = ? AND (expires_at IS NULL OR expires_at > ?))", "confirmed", "pending", s.Now()).Count(&count).Error
	return count > 0, err
}
func (s *Service) RescheduleAvailability(ctx context.Context, code, date string, start time.Time) ([]slot.Slot, error) {
	day, err := s.lookupDay([]string{date})
	if err != nil {
		return nil, err
	}
	db := s.DB.WithContext(ctx)
	r, err := s.findByCode(db, code, day)
	if err != nil {
		return nil, err
	}
	if !s.canReschedule(r) {
		return nil, ErrConflict
	}
	if err = s.Validate(s.rescheduleInput(r, r.SlotID, start)); err != nil {
		return nil, err
	}
	var slots []slot.Slot
	if err = db.Where("is_available = ?", true).Order("id").Find(&slots).Error; err != nil {
		return nil, err
	}
	free := make([]slot.Slot, 0, len(slots))
	for _, sl := range slots {
		busy, err := s.rescheduleBusy(db, r, sl.ID, start)
		if err != nil {
			return nil, err
		}
		if !busy {
			free = append(free, sl)
		}
	}
	return free, nil
}
func (s *Service) Reschedule(ctx context.Context, code, date string, slotID uint, start time.Time) (ReservationAccess, error) {
	day, err := s.lookupDay([]string{date})
	if err != nil {
		return ReservationAccess{}, err
	}
	original, err := s.findByCode(s.DB.WithContext(ctx), code, day)
	if err != nil {
		return ReservationAccess{}, err
	}
	var result ReservationAccess
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock old and new spaces in a stable order, then the booking, like payment/QR operations.
		ids := []uint{original.SlotID}
		if slotID != original.SlotID {
			ids = append(ids, slotID)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			locked := tx.Model(&slot.Slot{}).Where("id = ?", id).Update("updated_at", s.Now())
			if locked.Error != nil {
				return locked.Error
			}
			if locked.RowsAffected != 1 {
				return ErrConflict
			}
		}
		if err := tx.Model(&reservation.Reservation{}).Where("id = ?", original.ID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		r, err := s.findByCode(tx, code, day)
		if err != nil {
			return err
		}
		if r.SlotID != original.SlotID || !s.canReschedule(r) {
			return ErrConflict
		}
		if err = s.Validate(s.rescheduleInput(r, slotID, start)); err != nil {
			return err
		}
		var target slot.Slot
		if err = tx.Where("id = ? AND is_available = ?", slotID, true).First(&target).Error; err != nil {
			return ErrConflict
		}
		busy, err := s.rescheduleBusy(tx, r, slotID, start)
		if err != nil {
			return err
		}
		if busy {
			return ErrConflict
		}
		hash := r.CodeHash
		if r.CodeVersion == 2 {
			newHash := dailyCodeHash(start.In(Location).Format("2006-01-02"), ConfirmationCode(r))
			var count int64
			if err = tx.Model(&reservation.Reservation{}).Unscoped().Where("id <> ? AND code_hash = ?", r.ID, newHash).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				hash = nil
			} else {
				hash = &newHash
			}
		}
		// Keep the paid duration, token, checkout and status; move the booking atomically.
		if err = tx.Model(&r).Updates(map[string]any{"slot_id": slotID, "start_time": start.UTC(), "end_time": start.Add(time.Duration(r.DurationMinutes) * time.Minute).UTC(), "code_hash": hash}).Error; err != nil {
			return err
		}
		if err = EnsureConfirmation(tx, &r); err != nil {
			return err
		}
		result, err = s.access(tx, r)
		return err
	})
	return result, err
}
