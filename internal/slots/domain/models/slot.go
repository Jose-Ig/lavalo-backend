package models

import (
	"time"

	"gorm.io/gorm"
)

// Slot represents an available time slot for car wash service
type Slot struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	StartTime   time.Time      `gorm:"not null;index" json:"start_time"`
	EndTime     time.Time      `gorm:"not null" json:"end_time"`
	IsAvailable bool           `gorm:"default:true" json:"is_available"`
	MaxCapacity int            `gorm:"default:1" json:"max_capacity"`
	CurrentLoad int            `gorm:"default:0" json:"current_load"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName specifies the table name for Slot
func (Slot) TableName() string {
	return "slots"
}

// HasCapacity checks if the slot has available capacity
func (s *Slot) HasCapacity() bool {
	return s.IsAvailable && s.CurrentLoad < s.MaxCapacity
}

