package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/Jose-Ig/lavalo-backend/internal/common"
	reservationModels "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
)

const (
	// Business hours configuration
	startHour = 8
	endHour   = 22
	stepMins  = 30
	daysAhead = 7
)

// AvailabilityRepository defines the interface for availability data access
type AvailabilityRepository interface {
	// FindAllSlots returns all active physical slots
	FindAllSlots(ctx context.Context) ([]models.Slot, error)
	// FindReservationsByDateRange returns active reservations within a date range
	FindReservationsByDateRange(ctx context.Context, start, end time.Time) ([]reservationModels.Reservation, error)
}

// AvailabilityUseCase handles availability business logic
type AvailabilityUseCase struct {
	repo AvailabilityRepository
}

// NewAvailabilityUseCase creates a new availability use case
func NewAvailabilityUseCase(repo AvailabilityRepository) *AvailabilityUseCase {
	return &AvailabilityUseCase{
		repo: repo,
	}
}

// GetWeekAvailability returns availability for the next 7 days
func (uc *AvailabilityUseCase) GetWeekAvailability(ctx context.Context) (models.AvailabilityResponse, error) {
	// Get today's date at midnight (local time)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// Calculate date range: today -> today + 7 days
	startDate := today
	endDate := today.AddDate(0, 0, daysAhead).Add(24 * time.Hour) // End of day 7

	// Fetch all slots
	slots, err := uc.repo.FindAllSlots(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", common.ErrInternalServer, err)
	}

	// If no slots exist, return error
	if len(slots) == 0 {
		return nil, fmt.Errorf("%w: no slots configured", common.ErrNotFound)
	}

	// Fetch reservations in range
	reservations, err := uc.repo.FindReservationsByDateRange(ctx, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", common.ErrInternalServer, err)
	}

	// Build reservation lookup: map[date][slotID][hour] = true
	reservedMap := buildReservationMap(reservations)

	// Build response
	response := make(models.AvailabilityResponse)

	for d := 0; d <= daysAhead; d++ {
		currentDate := today.AddDate(0, 0, d)
		dateKey := currentDate.Format("2006-01-02")

		dayAvailability := models.DayAvailability{
			Slots: make([]models.SlotAvailability, 0, len(slots)),
			Hours: make([]models.HourAvailability, 0),
		}

		// Generate hours for the day
		hours := generateHours()

		// Check each slot's availability for this day
		for _, slot := range slots {
			// A slot is available for the day if at least one hour is free
			slotHasAvailability := false
			for _, hour := range hours {
				if !isReserved(reservedMap, dateKey, slot.ID, hour) {
					slotHasAvailability = true
					break
				}
			}

			dayAvailability.Slots = append(dayAvailability.Slots, models.SlotAvailability{
				ID:          slot.ID,
				Label:       slot.Label,
				IsAvailable: slot.IsAvailable && slotHasAvailability,
			})
		}

		// Check each hour's availability (available if at least one slot is free)
		for _, hour := range hours {
			hourHasAvailability := false
			for _, slot := range slots {
				if slot.IsAvailable && !isReserved(reservedMap, dateKey, slot.ID, hour) {
					hourHasAvailability = true
					break
				}
			}

			dayAvailability.Hours = append(dayAvailability.Hours, models.HourAvailability{
				Value:       hour,
				IsAvailable: hourHasAvailability,
			})
		}

		response[dateKey] = dayAvailability
	}

	return response, nil
}

// generateHours generates time slots from startHour to endHour with stepMins intervals
func generateHours() []string {
	hours := make([]string, 0)

	for h := startHour; h < endHour; h++ {
		for m := 0; m < 60; m += stepMins {
			hours = append(hours, fmt.Sprintf("%02d:%02d", h, m))
		}
	}

	return hours
}

// buildReservationMap creates a lookup map: map[date][slotID][hour] = true
func buildReservationMap(reservations []reservationModels.Reservation) map[string]map[uint]map[string]bool {
	result := make(map[string]map[uint]map[string]bool)

	for _, r := range reservations {
		// Only consider active reservations
		if !r.IsActive() {
			continue
		}

		dateKey := r.StartTime.Format("2006-01-02")
		hourKey := r.StartTime.Format("15:04")

		if result[dateKey] == nil {
			result[dateKey] = make(map[uint]map[string]bool)
		}
		if result[dateKey][r.SlotID] == nil {
			result[dateKey][r.SlotID] = make(map[string]bool)
		}

		result[dateKey][r.SlotID][hourKey] = true
	}

	return result
}

// isReserved checks if a slot is reserved at a specific date and hour
func isReserved(reservedMap map[string]map[uint]map[string]bool, date string, slotID uint, hour string) bool {
	if reservedMap[date] == nil {
		return false
	}
	if reservedMap[date][slotID] == nil {
		return false
	}
	return reservedMap[date][slotID][hour]
}

