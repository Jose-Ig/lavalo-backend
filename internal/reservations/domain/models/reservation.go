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
	ID        uint              `gorm:"primaryKey" json:"id"`
	UserID    uint              `gorm:"index;not null" json:"user_id"`
	SlotID    uint              `gorm:"index;not null" json:"slot_id"`
	AddressID uint              `gorm:"index;not null" json:"address_id"`
	Status    ReservationStatus `gorm:"type:varchar(20);default:'pending'" json:"status"`
	Notes     string            `gorm:"type:text" json:"notes,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	DeletedAt gorm.DeletedAt    `gorm:"index" json:"-"`
}

// TableName specifies the table name for Reservation
func (Reservation) TableName() string {
	return "reservations"
}

