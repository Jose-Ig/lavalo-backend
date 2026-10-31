package bookings

import (
	"context"
	"errors"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"testing"
	"time"
)

func TestAutomaticAssignmentUsesOnlySpacesFreeForEntireInterval(t *testing.T) {
	s, i := fixture(t)
	ctx := context.Background()
	if err := s.DB.Create(&slot.Slot{ID: 2, Label: "Two", IsAvailable: true}).Error; err != nil {
		t.Fatal(err)
	}
	first, _, err := s.Create(ctx, i)
	if err != nil {
		t.Fatal(err)
	}
	i.SlotID = 0
	i.StartTime = i.StartTime.Add(15 * time.Minute)
	i.Duration = 15
	second, _, err := s.Create(ctx, i)
	if err != nil || second.SlotID != 2 {
		t.Fatal("did not use remaining space", second, err)
	}
	if _, _, err = s.Create(ctx, i); !errors.Is(err, ErrConflict) {
		t.Fatal("full interval accepted", err)
	}
	if err = s.DB.Model(first).Update("expires_at", s.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	third, _, err := s.Create(ctx, i)
	if err != nil || third.SlotID != 1 {
		t.Fatal("expired hold blocked automatic assignment", third, err)
	}
}
func TestAutomaticAssignmentConcurrentRequestsUseDistinctSpaces(t *testing.T) {
	s, i := fixture(t)
	i.SlotID = 0
	if err := s.DB.Create(&slot.Slot{ID: 2, Label: "Two", IsAvailable: true}).Error; err != nil {
		t.Fatal(err)
	}
	type result struct {
		r   *reservation.Reservation
		err error
	}
	results := make(chan result, 3)
	for n := 0; n < 3; n++ {
		go func() { r, _, err := s.Create(context.Background(), i); results <- result{r, err} }()
	}
	occupied := map[uint]bool{}
	conflicts := 0
	for n := 0; n < 3; n++ {
		r := <-results
		if errors.Is(r.err, ErrConflict) {
			conflicts++
			continue
		}
		if r.err != nil {
			t.Fatal(r.err)
		}
		if occupied[r.r.SlotID] {
			t.Fatal("double assignment")
		}
		occupied[r.r.SlotID] = true
	}
	if len(occupied) != 2 || conflicts != 1 {
		t.Fatal(occupied, conflicts)
	}
}
func TestAutomaticAssignmentRejectsDisabledSpacesAndInvalidTimes(t *testing.T) {
	s, i := fixture(t)
	i.SlotID = 0
	if err := s.DB.Model(&slot.Slot{}).Where("id = 1").Update("is_available", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create(context.Background(), i); !errors.Is(err, ErrConflict) {
		t.Fatal("disabled space assigned", err)
	}
	i.Duration = 45
	if _, _, err := s.Create(context.Background(), i); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid duration accepted", err)
	}
}
func TestAutomaticRescheduleReassignsSpaceWithoutChangingPaidDuration(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	ctx := context.Background()
	code := ConfirmationCode(*r)
	day := r.StartTime.In(Location).Format("2006-01-02")
	if err := s.DB.Create(&slot.Slot{ID: 2, Label: "Two", IsAvailable: true}).Error; err != nil {
		t.Fatal(err)
	}
	target := r.EndTime
	if _, _, err := s.Create(ctx, Input{SlotID: r.SlotID, StartTime: target, Duration: r.DurationMinutes, Name: r.Name, Phone: r.Phone}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Reschedule(ctx, code, day, 0, target)
	if err != nil || got.SlotID != 2 || got.Duration != r.DurationMinutes || got.Code != code {
		t.Fatal(got, err)
	}
	got, err = s.Reschedule(ctx, code, day, 0, target.Add(15*time.Minute))
	if err != nil || got.SlotID != 2 {
		t.Fatal("current free space was not preserved", got, err)
	}
}
