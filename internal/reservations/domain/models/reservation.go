package models

import (
	"time"

	"gorm.io/gorm"
)

// ReservationStatus represents the status of a reservation
type ReservationStatus string

const (
	ReservationStatusPending   ReservationStatus = "pending"
	ReservationStatusConfirmed ReservationStatus = "confirmed"
	ReservationStatusCancelled ReservationStatus = "cancelled"
	ReservationStatusCompleted ReservationStatus = "completed"
)

// Reservation represents a car wash reservation
type Reservation struct {
	CodeHash        *string           `gorm:"uniqueIndex" json:"-"`
	WhatsAppOptIn   bool              `json:"whatsapp_opt_in"`
	QRVerifiedAt    *time.Time        `json:"qr_verified_at,omitempty"`
	Email           string            `gorm:"type:varchar(254)" json:"email,omitempty"`
	ID              uint              `gorm:"primaryKey" json:"id"`
	UserID          uint              `gorm:"index;not null" json:"user_id"`
	SlotID          uint              `gorm:"index;not null" json:"slot_id"`
	AddressID       uint              `gorm:"index;not null" json:"address_id"`
	EndTime         time.Time         `gorm:"index" json:"end_time"`
	DurationMinutes int               `json:"duration_minutes"`
	Name            string            `json:"name"`
	Phone           string            `json:"phone"`
	VehiclePlate    string            `json:"vehicle_plate"`
	VehicleModel    string            `json:"vehicle_model"`
	AccessTokenHash string            `json:"-"`
	ExpiresAt       *time.Time        `json:"expires_at,omitempty"`
	StartTime       time.Time         `gorm:"not null;index" json:"start_time"`
	Status          ReservationStatus `gorm:"type:varchar(20);default:'pending'" json:"status"`
	Notes           string            `gorm:"type:text" json:"notes,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	DeletedAt       gorm.DeletedAt    `gorm:"index" json:"-"`
}

// TableName specifies the table name for Reservation
func (Reservation) TableName() string {
	return "reservations"
}

// IsActive returns true if the reservation is not cancelled or completed
func (r *Reservation) IsActive() bool {
	return r.Status == ReservationStatusPending || r.Status == ReservationStatusConfirmed
}
