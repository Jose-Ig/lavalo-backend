package models

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func testSlotDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	return db
}

func TestPublicIDMigrationPreservesInternalKeysAndStableIdentifiers(t *testing.T) {
	db := testSlotDB(t)
	if err := db.Exec("CREATE TABLE slots (id integer PRIMARY KEY, label varchar(100) NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 4; id++ {
		if err := db.Exec("INSERT INTO slots (id, label) VALUES (?, ?)", id, fmt.Sprintf("Espacio %d", id)).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := PreparePublicIDs(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Slot{}); err != nil {
		t.Fatal(err)
	}
	var first []Slot
	if err := db.Order("id").Find(&first).Error; err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i, sl := range first {
		id, err := uuid.Parse(sl.PublicID)
		if err != nil || id.Version() != 4 || sl.ID != uint(i+1) || seen[sl.PublicID] {
			t.Fatalf("invalid migrated slot: %+v", sl)
		}
		seen[sl.PublicID] = true
	}
	if len(first) != 4 {
		t.Fatal("lost slots", len(first))
	}
	if err := PreparePublicIDs(db); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Slot{}); err != nil {
		t.Fatal(err)
	}
	var second []Slot
	db.Order("id").Find(&second)
	for i, sl := range second {
		if sl.PublicID != first[i].PublicID {
			t.Fatal("restart rotated a QR identifier")
		}
	}
	if err := db.Create(&Slot{Label: "Duplicate", PublicID: first[0].PublicID}).Error; err == nil {
		t.Fatal("duplicate public ID accepted")
	}
	if err := db.Exec("INSERT INTO slots (label, public_id) VALUES ('Missing UUID', NULL)").Error; err == nil {
		t.Fatal("null public ID accepted")
	}
}

func TestNewSlotsReceiveDistinctUUIDs(t *testing.T) {
	db := testSlotDB(t)
	if err := db.AutoMigrate(&Slot{}); err != nil {
		t.Fatal(err)
	}
	a, b := Slot{Label: "A"}, Slot{Label: "B"}
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	if a.PublicID == "" || a.PublicID == b.PublicID {
		t.Fatal("missing or repeated UUID")
	}
	if err := db.Create(&Slot{Label: "Invalid", PublicID: "1"}).Error; err == nil {
		t.Fatal("numeric public ID accepted")
	}
}
