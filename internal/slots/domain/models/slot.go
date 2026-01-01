package models

import (
	"time"

	"gorm.io/gorm"
)

// Slot represents a physical space/bay for car wash service (e.g., "Espacio 1", "Espacio 2")
type Slot struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Label       string         `gorm:"type:varchar(100);not null" json:"label"`
	IsAvailable bool           `gorm:"default:true" json:"is_available"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName specifies the table name for Slot
func (Slot) TableName() string {
	return "slots"
}
