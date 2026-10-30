package payments

import "time"

// Checkout is a durable payment attempt. ReservationID prevents duplicate preferences.
// Legacy payments are retained unchanged; all new money amounts are integer cents.
type Checkout struct {
	ReconciledAt  *time.Time `gorm:"index" json:"-"`
	ID            uint       `gorm:"primaryKey" json:"id"`
	ReservationID uint       `gorm:"uniqueIndex;not null" json:"reservation_id"`
	Reference     string     `gorm:"uniqueIndex;not null" json:"-"`
	AmountCents   int64      `gorm:"not null" json:"amount_cents"`
	Currency      string     `json:"currency"`
	PreferenceID  string     `json:"-"`
	URL           string     `json:"-"`
	State         string     `json:"status"`
	Issue         string     `json:"issue,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"-"`
	UpdatedAt     time.Time  `json:"-"`
}

// Receipt stores each provider payment separately, including multiple payments on one preference.
type Receipt struct {
	ID                uint   `gorm:"primaryKey"`
	CheckoutID        uint   `gorm:"index;not null"`
	ProviderID        string `gorm:"uniqueIndex;not null"`
	Status            string
	StatusDetail      string
	ProviderUpdatedAt time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
