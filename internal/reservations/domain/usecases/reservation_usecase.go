package usecases

import (
	"context"

	"github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
)

// ReservationRepository defines the interface for reservation data access
type ReservationRepository interface {
	FindAll(ctx context.Context) ([]models.Reservation, error)
	FindByID(ctx context.Context, id uint) (*models.Reservation, error)
	FindByUserID(ctx context.Context, userID uint) ([]models.Reservation, error)
	Create(ctx context.Context, reservation *models.Reservation) error
	Update(ctx context.Context, reservation *models.Reservation) error
	Delete(ctx context.Context, id uint) error
}

// ReservationUseCase handles reservation business logic
type ReservationUseCase struct {
	repo ReservationRepository
}

// NewReservationUseCase creates a new reservation use case
func NewReservationUseCase(repo ReservationRepository) *ReservationUseCase {
	return &ReservationUseCase{
		repo: repo,
	}
}

// TODO: Implement use case methods
// - CreateReservation
// - GetReservation
// - ListReservations
// - UpdateReservation
// - CancelReservation

