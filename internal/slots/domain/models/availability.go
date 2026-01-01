package models

// AvailabilityResponse is the response shape for GET /availability
// Map key is date string in format "YYYY-MM-DD"
type AvailabilityResponse map[string]DayAvailability

// DayAvailability represents availability for a single day
type DayAvailability struct {
	Slots []SlotAvailability `json:"slots"`
	Hours []HourAvailability `json:"hours"`
}

// SlotAvailability represents a physical space availability
type SlotAvailability struct {
	ID          uint   `json:"id"`
	Label       string `json:"label"`
	IsAvailable bool   `json:"is_available"`
}

// HourAvailability represents a time slot availability
type HourAvailability struct {
	Value       string `json:"value"`
	IsAvailable bool   `json:"is_available"`
}

