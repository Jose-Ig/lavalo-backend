package bookings

import (
	"context"
	"errors"
	"fmt"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Service, Input) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	if err = db.AutoMigrate(&reservation.Reservation{}, &slot.Slot{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&slot.Slot{ID: 1, Label: "One", IsAvailable: true}).Error; err != nil {
		t.Fatal(err)
	}
	s := New(db)
	now := time.Date(2026, 10, 28, 8, 0, 0, 0, Location)
	s.Now = func() time.Time { return now }
	return s, Input{SlotID: 1, StartTime: now.Add(2 * time.Hour), Duration: 60, Name: "Test User", Phone: "+543511234567"}
}
func TestOverlapAdjacentAndPrivateAccess(t *testing.T) {
	s, i := fixture(t)
	ctx := context.Background()
	r, token, err := s.Create(ctx, i)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "pending" || r.EndTime.Sub(r.StartTime) != time.Hour {
		t.Fatal("invalid reservation")
	}
	if _, err = s.Get(ctx, r.ID, "wrong"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("unguarded access")
	}
	if _, err = s.Get(ctx, r.ID, token); err != nil {
		t.Fatal(err)
	}
	i.StartTime = i.StartTime.Add(15 * time.Minute)
	if _, _, err = s.Create(ctx, i); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlap accepted: %v", err)
	}
	i.StartTime = i.StartTime.Add(45 * time.Minute)
	if _, _, err = s.Create(ctx, i); err != nil {
		t.Fatalf("adjacent rejected: %v", err)
	}
	if err = s.Cancel(ctx, r.ID, token); err != nil {
		t.Fatal(err)
	}
	i.StartTime = r.StartTime
	if _, _, err = s.Create(ctx, i); err != nil {
		t.Fatal("cancel did not release slot", err)
	}
}
func TestExpiredHoldAndDurationAvailability(t *testing.T) {
	s, i := fixture(t)
	ctx := context.Background()
	_, _, err := s.Create(ctx, i)
	if err != nil {
		t.Fatal(err)
	}
	free, err := s.Available(ctx, i.StartTime.Add(30*time.Minute), 15)
	if err != nil || len(free) != 0 {
		t.Fatal("overlapping slot advertised", err)
	}
	now := s.Now().Add(11 * time.Minute)
	s.Now = func() time.Time { return now }
	if _, _, err = s.Create(ctx, i); err != nil {
		t.Fatal("expired hold blocks slot", err)
	}
}
func TestInvalidBookings(t *testing.T) {
	s, base := fixture(t)
	cases := []Input{base, base, base, base, base}
	cases[0].Duration = 45
	cases[1].StartTime = s.Now().Add(-time.Hour)
	cases[2].StartTime = time.Date(2026, 10, 28, 20, 30, 0, 0, Location)
	cases[3].Name = ""
	cases[4].StartTime = base.StartTime.Add(time.Minute)
	for _, i := range cases {
		if _, _, err := s.Create(context.Background(), i); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid input accepted: %+v %v", i, err)
		}
	}
}
func TestConcurrentCreate(t *testing.T) {
	s, i := fixture(t)
	results := make(chan error, 2)
	for n := 0; n < 2; n++ {
		go func() { _, _, err := s.Create(context.Background(), i); results <- err }()
	}
	successes, conflicts := 0, 0
	for n := 0; n < 2; n++ {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", successes, conflicts)
	}
}
