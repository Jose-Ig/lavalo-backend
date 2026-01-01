package repositories

import (
	"context"

	"github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"gorm.io/gorm"
)

// ReservationRepository implements the reservation repository interface
type ReservationRepository struct {
	db *gorm.DB
}

// NewReservationRepository creates a new reservation repository
func NewReservationRepository(db *gorm.DB) *ReservationRepository {
	return &ReservationRepository{
		db: db,
	}
}

// FindAll retrieves all reservations
func (r *ReservationRepository) FindAll(ctx context.Context) ([]models.Reservation, error) {
	var reservations []models.Reservation
	if err := r.db.WithContext(ctx).Find(&reservations).Error; err != nil {
		return nil, err
	}
	return reservations, nil
}

// FindByID retrieves a reservation by ID
func (r *ReservationRepository) FindByID(ctx context.Context, id uint) (*models.Reservation, error) {
	var reservation models.Reservation
	if err := r.db.WithContext(ctx).First(&reservation, id).Error; err != nil {
		return nil, err
	}
	return &reservation, nil
}

// FindByUserID retrieves all reservations for a user
func (r *ReservationRepository) FindByUserID(ctx context.Context, userID uint) ([]models.Reservation, error) {
	var reservations []models.Reservation
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&reservations).Error; err != nil {
		return nil, err
	}
	return reservations, nil
}

// Create creates a new reservation
func (r *ReservationRepository) Create(ctx context.Context, reservation *models.Reservation) error {
	return r.db.WithContext(ctx).Create(reservation).Error
}

// Update updates an existing reservation
func (r *ReservationRepository) Update(ctx context.Context, reservation *models.Reservation) error {
	return r.db.WithContext(ctx).Save(reservation).Error
}

// Delete soft deletes a reservation
func (r *ReservationRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&models.Reservation{}, id).Error
}

