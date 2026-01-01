package models

import (
	"time"

	"gorm.io/gorm"
)

// Address represents a user's service address
type Address struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	UserID       uint           `gorm:"index;not null" json:"user_id"`
	Street       string         `gorm:"type:varchar(255);not null" json:"street"`
	Number       string         `gorm:"type:varchar(20)" json:"number"`
	Apartment    string         `gorm:"type:varchar(50)" json:"apartment,omitempty"`
	City         string         `gorm:"type:varchar(100);not null" json:"city"`
	State        string         `gorm:"type:varchar(100)" json:"state"`
	ZipCode      string         `gorm:"type:varchar(20)" json:"zip_code"`
	Country      string         `gorm:"type:varchar(100);default:'Argentina'" json:"country"`
	Latitude     float64        `gorm:"type:decimal(10,8)" json:"latitude,omitempty"`
	Longitude    float64        `gorm:"type:decimal(11,8)" json:"longitude,omitempty"`
	Instructions string         `gorm:"type:text" json:"instructions,omitempty"`
	IsDefault    bool           `gorm:"default:false" json:"is_default"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName specifies the table name for Address
func (Address) TableName() string {
	return "addresses"
}

