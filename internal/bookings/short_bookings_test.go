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
	input.StartTime = time.Date(2026, 10, 28, 23, 55, 0, 0, Location)
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

func TestLateBookingsMustFitBefore2359(t *testing.T) {
	s, input := fixture(t)
	for _, tc := range []struct {
		hour, minute, duration int
		valid                  bool
	}{
		{21, 0, 60, true}, {22, 55, 60, true}, {23, 0, 60, false},
		{23, 25, 30, true}, {23, 30, 30, false}, {23, 40, 15, true},
		{23, 45, 15, false}, {23, 55, 3, true}, {23, 59, 3, false},
	} {
		input.StartTime = time.Date(2026, 10, 28, tc.hour, tc.minute, 0, 0, Location)
		input.Duration = tc.duration
		err := s.Validate(input)
		if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrInvalid) {
			t.Fatalf("%02d:%02d duration %d: %v", tc.hour, tc.minute, tc.duration, err)
		}
	}
}
