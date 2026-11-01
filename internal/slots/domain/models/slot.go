package models

import (
	"encoding/json"
	"regexp"
	"time"

	"gorm.io/gorm"
)

// Slot represents a physical space/bay for car wash service (e.g., "Espacio N° 1", "Espacio N° 2")
type Slot struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	PublicID    string         `gorm:"type:varchar(36);not null;uniqueIndex:idx_slots_public_id" json:"public_id"`
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

var numberedSpace = regexp.MustCompile(`(?i)^espacio\s+(?:n[°º]\s*)?(\d+)$`)

// DisplayLabel keeps stored labels and public UUIDs intact while normalizing customer copy.
func (s Slot) DisplayLabel() string { return numberedSpace.ReplaceAllString(s.Label, "Espacio N° $1") }
func (s Slot) MarshalJSON() ([]byte, error) {
	type plain Slot
	return json.Marshal(struct {
		plain
		Label string `json:"label"`
	}{plain(s), s.DisplayLabel()})
}
