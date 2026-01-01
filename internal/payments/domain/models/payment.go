package models

import (
	"time"

	"gorm.io/gorm"
)

// PaymentStatus represents the status of a payment
type PaymentStatus string

const (
	PaymentStatusPending   PaymentStatus = "pending"
	PaymentStatusCompleted PaymentStatus = "completed"
	PaymentStatusFailed    PaymentStatus = "failed"
	PaymentStatusRefunded  PaymentStatus = "refunded"
)

// Payment represents a payment transaction
type Payment struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	ReservationID uint           `gorm:"index;not null" json:"reservation_id"`
	Amount        float64        `gorm:"type:decimal(10,2);not null" json:"amount"`
	Currency      string         `gorm:"type:varchar(3);default:'ARS'" json:"currency"`
	Status        PaymentStatus  `gorm:"type:varchar(20);default:'pending'" json:"status"`
	Provider      string         `gorm:"type:varchar(50)" json:"provider"`
	ExternalID    string         `gorm:"type:varchar(255)" json:"external_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName specifies the table name for Payment
func (Payment) TableName() string {
	return "payments"
}

