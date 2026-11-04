package bookings

import "errors"

// PriceCents is the authoritative ARS tariff for bookings and time extensions.
func PriceCents(minutes int) (int64, error) {
	switch minutes {
	case 3:
		return 25000, nil
	case 15:
		return 125000, nil
	case 30:
		return 250000, nil
	case 60:
		return 500000, nil
	}
	return 0, errors.New("unsupported duration")
}
