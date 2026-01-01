package repositories

import (
	"context"

	"github.com/Jose-Ig/lavalo-backend/internal/payments/domain/models"
	"gorm.io/gorm"
)

// PaymentRepository implements the payment repository interface
type PaymentRepository struct {
	db *gorm.DB
}

// NewPaymentRepository creates a new payment repository
func NewPaymentRepository(db *gorm.DB) *PaymentRepository {
	return &PaymentRepository{
		db: db,
	}
}

// FindAll retrieves all payments
func (r *PaymentRepository) FindAll(ctx context.Context) ([]models.Payment, error) {
	var payments []models.Payment
	if err := r.db.WithContext(ctx).Find(&payments).Error; err != nil {
		return nil, err
	}
	return payments, nil
}

// FindByID retrieves a payment by ID
func (r *PaymentRepository) FindByID(ctx context.Context, id uint) (*models.Payment, error) {
	var payment models.Payment
	if err := r.db.WithContext(ctx).First(&payment, id).Error; err != nil {
		return nil, err
	}
	return &payment, nil
}

// FindByReservationID retrieves payments for a reservation
func (r *PaymentRepository) FindByReservationID(ctx context.Context, reservationID uint) ([]models.Payment, error) {
	var payments []models.Payment
	if err := r.db.WithContext(ctx).Where("reservation_id = ?", reservationID).Find(&payments).Error; err != nil {
		return nil, err
	}
	return payments, nil
}

// Create creates a new payment
func (r *PaymentRepository) Create(ctx context.Context, payment *models.Payment) error {
	return r.db.WithContext(ctx).Create(payment).Error
}

// Update updates an existing payment
func (r *PaymentRepository) Update(ctx context.Context, payment *models.Payment) error {
	return r.db.WithContext(ctx).Save(payment).Error
}

