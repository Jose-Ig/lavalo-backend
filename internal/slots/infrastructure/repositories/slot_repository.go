package repositories

import (
	"context"
	"time"

	"github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"gorm.io/gorm"
)

// SlotRepository implements the slot repository interface
type SlotRepository struct {
	db *gorm.DB
}

// NewSlotRepository creates a new slot repository
func NewSlotRepository(db *gorm.DB) *SlotRepository {
	return &SlotRepository{
		db: db,
	}
}

// FindAll retrieves all slots
func (r *SlotRepository) FindAll(ctx context.Context) ([]models.Slot, error) {
	var slots []models.Slot
	if err := r.db.WithContext(ctx).Find(&slots).Error; err != nil {
		return nil, err
	}
	return slots, nil
}

// FindByID retrieves a slot by ID
func (r *SlotRepository) FindByID(ctx context.Context, id uint) (*models.Slot, error) {
	var slot models.Slot
	if err := r.db.WithContext(ctx).First(&slot, id).Error; err != nil {
		return nil, err
	}
	return &slot, nil
}

// FindAvailable retrieves available slots within a time range
func (r *SlotRepository) FindAvailable(ctx context.Context, startTime, endTime time.Time) ([]models.Slot, error) {
	var slots []models.Slot
	if err := r.db.WithContext(ctx).
		Where("is_available = ? AND start_time >= ? AND end_time <= ?", true, startTime, endTime).
		Where("current_load < max_capacity").
		Order("start_time ASC").
		Find(&slots).Error; err != nil {
		return nil, err
	}
	return slots, nil
}

// Create creates a new slot
func (r *SlotRepository) Create(ctx context.Context, slot *models.Slot) error {
	return r.db.WithContext(ctx).Create(slot).Error
}

// Update updates an existing slot
func (r *SlotRepository) Update(ctx context.Context, slot *models.Slot) error {
	return r.db.WithContext(ctx).Save(slot).Error
}

// Delete soft deletes a slot
func (r *SlotRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&models.Slot{}, id).Error
}

