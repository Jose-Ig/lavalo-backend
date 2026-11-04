package bookings

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestThreeMinuteBookingOnFiveMinuteGrid(t *testing.T) {
	s, input := fixture(t)
	ctx := context.Background()
	input.Duration = 3
	input.StartTime = input.StartTime.Add(5 * time.Minute)
	r, _, err := s.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if r.EndTime.Sub(r.StartTime) != 3*time.Minute {
		t.Fatal("wrong end", r.EndTime)
	}
	free, err := s.Available(ctx, r.StartTime, 3)
	if err != nil || len(free) != 0 {
		t.Fatal("overlap advertised", free, err)
	}
	input.StartTime = r.StartTime.Add(5 * time.Minute)
	if _, _, err = s.Create(ctx, input); err != nil {
		t.Fatal("next five-minute start rejected", err)
	}
	input.StartTime = r.StartTime.Add(time.Minute)
	if err = s.Validate(input); !errors.Is(err, ErrInvalid) {
		t.Fatal("off-grid start accepted", err)
	}
	input.StartTime = time.Date(2026, 10, 28, 20, 55, 0, 0, Location)
	if err = s.Validate(input); err != nil {
		t.Fatal("last short turn rejected", err)
	}
	input.StartTime = input.StartTime.Add(5 * time.Minute)
	if err = s.Validate(input); !errors.Is(err, ErrInvalid) {
		t.Fatal("closing ignored", err)
	}
	input.StartTime = r.StartTime
	input.Duration = 2
	if err = s.Validate(input); !errors.Is(err, ErrInvalid) {
		t.Fatal("below minimum accepted", err)
	}
}
