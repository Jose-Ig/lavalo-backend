package payments

import "github.com/Jose-Ig/lavalo-backend/internal/bookings"

// PriceCents is the authoritative ARS tariff; never accept amounts from the client.
func PriceCents(duration int) (int64, error) { return bookings.PriceCents(duration) }
