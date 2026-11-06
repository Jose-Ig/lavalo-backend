package repositories

import (
	"context"
	"github.com/Jose-Ig/lavalo-backend/internal/operations"
	"time"

	reservationModels "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"gorm.io/gorm"
)

// AvailabilityRepository implements the availability repository interface
type AvailabilityRepository struct {
	db *gorm.DB
}

// NewAvailabilityRepository creates a new availability repository
func NewAvailabilityRepository(db *gorm.DB) *AvailabilityRepository {
	return &AvailabilityRepository{
		db: db,
	}
}

// FindAllSlots returns all active physical slots
func (r *AvailabilityRepository) FindAllSlots(ctx context.Context) ([]models.Slot, error) {
	var slots []models.Slot
	if err := r.db.WithContext(ctx).
		Where("is_available = ?", true).
		Order("id ASC").
		Find(&slots).Error; err != nil {
		return nil, err
	}
	return slots, nil
}

// FindReservationsByDateRange returns active reservations within a date range
func (r *AvailabilityRepository) FindReservationsByDateRange(ctx context.Context, start, end time.Time) ([]reservationModels.Reservation, error) {
	var reservations []reservationModels.Reservation

	// Get reservations that are pending or confirmed within the date range
	if err := r.db.WithContext(ctx).
		Where("start_time < ? AND (end_time > ? OR (end_time IS NULL AND start_time >= ?))", end, start, start).
		Where("status IN ?", []string{
			string(reservationModels.ReservationStatusPending),
			string(reservationModels.ReservationStatusConfirmed),
		}).
		Find(&reservations).Error; err != nil {
		return nil, err
	}

	// Include operational intervals without changing commercial reservation data.
	var sessions []operations.Session
	if err := r.db.WithContext(ctx).Where("state IN ? AND start_ms < ? AND (end_ms + extra_ms) > ?", []string{operations.Running, operations.Recovering, operations.Lost}, end.UnixMilli(), start.UnixMilli()).Find(&sessions).Error; err != nil {
		return nil, err
	}
	for _, session := range sessions {
		reservations = append(reservations, reservationModels.Reservation{SlotID: session.SlotID, StartTime: time.UnixMilli(session.StartMS), EndTime: time.UnixMilli(session.EndMS + session.ExtraMS), Status: reservationModels.ReservationStatusConfirmed})
	}
	return reservations, nil
}
