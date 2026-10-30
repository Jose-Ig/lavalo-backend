package payments

import "errors"

// PriceCents is the authoritative ARS tariff; never accept amounts from the client.
func PriceCents(duration int) (int64, error) {
	switch duration {
	case 15:
		return 125000, nil
	case 30:
		return 250000, nil
	case 60:
		return 500000, nil
	default:
		return 0, errors.New("unsupported duration")
	}
}
