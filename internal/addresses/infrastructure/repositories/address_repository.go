package repositories

import (
	"context"

	"github.com/Jose-Ig/lavalo-backend/internal/addresses/domain/models"
	"gorm.io/gorm"
)

// AddressRepository implements the address repository interface
type AddressRepository struct {
	db *gorm.DB
}

// NewAddressRepository creates a new address repository
func NewAddressRepository(db *gorm.DB) *AddressRepository {
	return &AddressRepository{
		db: db,
	}
}

// FindAll retrieves all addresses
func (r *AddressRepository) FindAll(ctx context.Context) ([]models.Address, error) {
	var addresses []models.Address
	if err := r.db.WithContext(ctx).Find(&addresses).Error; err != nil {
		return nil, err
	}
	return addresses, nil
}

// FindByID retrieves an address by ID
func (r *AddressRepository) FindByID(ctx context.Context, id uint) (*models.Address, error) {
	var address models.Address
	if err := r.db.WithContext(ctx).First(&address, id).Error; err != nil {
		return nil, err
	}
	return &address, nil
}

// FindByUserID retrieves all addresses for a user
func (r *AddressRepository) FindByUserID(ctx context.Context, userID uint) ([]models.Address, error) {
	var addresses []models.Address
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&addresses).Error; err != nil {
		return nil, err
	}
	return addresses, nil
}

// Create creates a new address
func (r *AddressRepository) Create(ctx context.Context, address *models.Address) error {
	return r.db.WithContext(ctx).Create(address).Error
}

// Update updates an existing address
func (r *AddressRepository) Update(ctx context.Context, address *models.Address) error {
	return r.db.WithContext(ctx).Save(address).Error
}

// Delete soft deletes an address
func (r *AddressRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&models.Address{}, id).Error
}

