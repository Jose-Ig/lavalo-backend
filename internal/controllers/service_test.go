package controllers

import (
	"fmt"
	"github.com/Jose-Ig/lavalo-backend/internal/operations"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Service, *slot.Slot, *time.Time) {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := db.DB()
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	if e = db.AutoMigrate(&operations.Session{}, &operations.Execution{}, &operations.Incident{}, &operations.Boot{}, &Device{}, &reservation.Reservation{}, &slot.Slot{}); e != nil {
		t.Fatal(e)
	}
	sl := &slot.Slot{Label: "Espacio N° 1", IsAvailable: true}
	if e = db.Create(sl).Error; e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC)
	s := New(db)
	s.Now = func() time.Time { return now }
	return s, sl, &now
}
func checkedIn(t *testing.T, s *Service, sl *slot.Slot) reservation.Reservation {
	t.Helper()
	n := s.Now()
	r := reservation.Reservation{SlotID: sl.ID, Status: "confirmed", StartTime: n.Add(-time.Minute), EndTime: n.Add(14 * time.Minute), QRVerifiedAt: &n, Email: "private@example.com", Phone: "private-phone"}
	if e := s.DB.Create(&r).Error; e != nil {
		t.Fatal(e)
	}
	return r
}
