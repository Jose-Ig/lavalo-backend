package models

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BeforeCreate assigns a stable, unpredictable public identifier without changing the internal key.
func (s *Slot) BeforeCreate(tx *gorm.DB) error {
	if s.PublicID == "" {
		id, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		s.PublicID = id.String()
	}
	id, err := uuid.Parse(s.PublicID)
	if err != nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || id.String() != s.PublicID {
		return fmt.Errorf("slot public_id must be a canonical UUID v4")
	}
	return nil
}

// The transitional column is nullable so existing rows can be backfilled before
// AutoMigrate adds the NOT NULL constraint and unique index from Slot.
type slotPublicIDColumn struct {
	PublicID string `gorm:"type:varchar(36)"`
}

func (slotPublicIDColumn) TableName() string { return "slots" }

func PreparePublicIDs(db *gorm.DB) error {
	if !db.Migrator().HasTable(&Slot{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if !tx.Migrator().HasColumn(&Slot{}, "PublicID") {
			if err := tx.Migrator().AddColumn(&slotPublicIDColumn{}, "PublicID"); err != nil {
				return err
			}
		}
		var slots []Slot
		if err := tx.Unscoped().Select("id", "public_id").Find(&slots).Error; err != nil {
			return err
		}
		for _, sl := range slots {
			if sl.PublicID != "" {
				continue
			}
			id, err := uuid.NewRandom()
			if err != nil {
				return err
			}
			// A concurrent startup must not replace an identifier already assigned.
			if err := tx.Unscoped().Model(&Slot{}).Where("id = ? AND (public_id IS NULL OR public_id = '')", sl.ID).
				UpdateColumn("public_id", id.String()).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
