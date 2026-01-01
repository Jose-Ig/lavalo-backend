package test

import (
	"context"
	"testing"
	"time"

	reservationModels "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/Jose-Ig/lavalo-backend/internal/slots/domain/usecases"
)

// mockAvailabilityRepository is a mock implementation of AvailabilityRepository
type mockAvailabilityRepository struct {
	slots        []models.Slot
	reservations []reservationModels.Reservation
}

func (m *mockAvailabilityRepository) FindAllSlots(ctx context.Context) ([]models.Slot, error) {
	return m.slots, nil
}

func (m *mockAvailabilityRepository) FindReservationsByDateRange(ctx context.Context, start, end time.Time) ([]reservationModels.Reservation, error) {
	return m.reservations, nil
}

func TestGetWeekAvailability_NoSlots(t *testing.T) {
	repo := &mockAvailabilityRepository{
		slots:        []models.Slot{},
		reservations: []reservationModels.Reservation{},
	}

	uc := usecases.NewAvailabilityUseCase(repo)

	_, err := uc.GetWeekAvailability(context.Background())
	if err == nil {
		t.Error("expected error when no slots exist, got nil")
	}
}

func TestGetWeekAvailability_AllAvailable(t *testing.T) {
	repo := &mockAvailabilityRepository{
		slots: []models.Slot{
			{ID: 1, Label: "Espacio 1", IsAvailable: true},
			{ID: 2, Label: "Espacio 2", IsAvailable: true},
		},
		reservations: []reservationModels.Reservation{},
	}

	uc := usecases.NewAvailabilityUseCase(repo)

	availability, err := uc.GetWeekAvailability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 8 days (today + 7)
	if len(availability) != 8 {
		t.Errorf("expected 8 days, got %d", len(availability))
	}

	// Check today's availability
	today := time.Now().Format("2006-01-02")
	dayAvail, ok := availability[today]
	if !ok {
		t.Fatalf("expected availability for today (%s), not found", today)
	}

	// Should have 2 slots
	if len(dayAvail.Slots) != 2 {
		t.Errorf("expected 2 slots, got %d", len(dayAvail.Slots))
	}

	// All slots should be available
	for _, slot := range dayAvail.Slots {
		if !slot.IsAvailable {
			t.Errorf("slot %d should be available", slot.ID)
		}
	}

	// Should have 28 hours (8:00-22:00, 30min intervals = 14 hours * 2)
	expectedHours := 28
	if len(dayAvail.Hours) != expectedHours {
		t.Errorf("expected %d hours, got %d", expectedHours, len(dayAvail.Hours))
	}

	// All hours should be available
	for _, hour := range dayAvail.Hours {
		if !hour.IsAvailable {
			t.Errorf("hour %s should be available", hour.Value)
		}
	}
}

func TestGetWeekAvailability_WithReservations(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 0, 0, now.Location())

	repo := &mockAvailabilityRepository{
		slots: []models.Slot{
			{ID: 1, Label: "Espacio 1", IsAvailable: true},
			{ID: 2, Label: "Espacio 2", IsAvailable: true},
		},
		reservations: []reservationModels.Reservation{
			{
				ID:        1,
				SlotID:    1,
				StartTime: today,
				Status:    reservationModels.ReservationStatusConfirmed,
			},
		},
	}

	uc := usecases.NewAvailabilityUseCase(repo)

	availability, err := uc.GetWeekAvailability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	todayKey := today.Format("2006-01-02")
	dayAvail := availability[todayKey]

	// First hour (08:00) should still be available because slot 2 is free
	firstHour := dayAvail.Hours[0]
	if firstHour.Value != "08:00" {
		t.Errorf("first hour should be 08:00, got %s", firstHour.Value)
	}
	if !firstHour.IsAvailable {
		t.Error("08:00 should be available (slot 2 is free)")
	}

	// Both slots should still show as available for the day
	// because they have other hours free
	for _, slot := range dayAvail.Slots {
		if !slot.IsAvailable {
			t.Errorf("slot %d should be available for the day", slot.ID)
		}
	}
}

func TestGetWeekAvailability_HourBlockedWhenAllSlotsReserved(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 0, 0, now.Location())

	repo := &mockAvailabilityRepository{
		slots: []models.Slot{
			{ID: 1, Label: "Espacio 1", IsAvailable: true},
			{ID: 2, Label: "Espacio 2", IsAvailable: true},
		},
		reservations: []reservationModels.Reservation{
			{
				ID:        1,
				SlotID:    1,
				StartTime: today,
				Status:    reservationModels.ReservationStatusConfirmed,
			},
			{
				ID:        2,
				SlotID:    2,
				StartTime: today,
				Status:    reservationModels.ReservationStatusPending,
			},
		},
	}

	uc := usecases.NewAvailabilityUseCase(repo)

	availability, err := uc.GetWeekAvailability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	todayKey := today.Format("2006-01-02")
	dayAvail := availability[todayKey]

	// First hour (08:00) should NOT be available (both slots reserved)
	firstHour := dayAvail.Hours[0]
	if firstHour.IsAvailable {
		t.Error("08:00 should NOT be available (both slots reserved)")
	}

	// Second hour (08:30) should be available
	secondHour := dayAvail.Hours[1]
	if !secondHour.IsAvailable {
		t.Error("08:30 should be available")
	}
}

func TestGetWeekAvailability_CancelledReservationsIgnored(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 0, 0, now.Location())

	repo := &mockAvailabilityRepository{
		slots: []models.Slot{
			{ID: 1, Label: "Espacio 1", IsAvailable: true},
		},
		reservations: []reservationModels.Reservation{
			{
				ID:        1,
				SlotID:    1,
				StartTime: today,
				Status:    reservationModels.ReservationStatusCancelled,
			},
		},
	}

	uc := usecases.NewAvailabilityUseCase(repo)

	availability, err := uc.GetWeekAvailability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	todayKey := today.Format("2006-01-02")
	dayAvail := availability[todayKey]

	// First hour should be available (cancelled reservation ignored)
	firstHour := dayAvail.Hours[0]
	if !firstHour.IsAvailable {
		t.Error("08:00 should be available (cancelled reservation should be ignored)")
	}
}

