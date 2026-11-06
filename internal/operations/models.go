// Package operations stores equipment time independently of commercial bookings.
package operations

import (
	"errors"
	"time"

	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"gorm.io/gorm"
)

const (
	Running      = "running"
	Lost         = "communication_lost"
	Recovering   = "recovering"
	Pending      = "pending_recovery"
	Completed    = "completed"
	Closed       = "closed"
	OnlineWindow = 12 * time.Second
)

type Session struct {
	ID              string `gorm:"primaryKey;size:36"`
	ReservationID   uint   `gorm:"uniqueIndex;not null"`
	SlotID          uint   `gorm:"index;not null"`
	DeviceID        string `gorm:"index;size:36;not null"`
	ExecutionID     string `gorm:"size:36"`
	State           string `gorm:"index;not null"`
	Reason          string
	Version         uint64
	PaidMinutes     int
	ExtraMS         int64
	StartMS         int64
	EndMS           int64 `gorm:"index"`
	RemainingMS     int64
	LastRemainingMS int64
	LastReportAt    *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Execution struct {
	ID                string `gorm:"primaryKey;size:36"`
	SessionID         string `gorm:"index;size:36;not null"`
	BootID            string `gorm:"size:36;not null"`
	CommandID         string `gorm:"uniqueIndex;size:36;not null"`
	Revision          uint64
	StartMS           int64
	EndMS             int64
	BudgetMS          int64
	SourceExecutionID string `gorm:"size:36"`
	SourceRemainingMS int64
	AdditionalMS      int64
	Sequence          uint64
	RemainingMS       int64
	Completed         bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Incident struct {
	ID           uint   `gorm:"primaryKey"`
	SessionID    string `gorm:"index;size:36;not null"`
	ExecutionID  string `gorm:"uniqueIndex;size:36;not null"`
	LastReportAt *time.Time
	DetectedAt   time.Time
	FrozenMS     int64
	Resolution   string
	ResolvedAt   *time.Time
}

// Boot also rejects requests from previously retired controller processes.
type Boot struct {
	ID        uint   `gorm:"primaryKey"`
	DeviceID  string `gorm:"uniqueIndex:device_boot;size:36;not null"`
	BootID    string `gorm:"uniqueIndex:device_boot;size:36;not null"`
	CreatedAt time.Time
}

func Terminal(state string) bool { return state == Completed || state == Closed }

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func Find(db *gorm.DB, reservationID uint) (Session, bool, error) {
	var s Session
	result := db.Where("reservation_id = ?", reservationID).Limit(1).Find(&s)
	err := result.Error
	if result.RowsAffected == 0 && err == nil {
		return s, false, nil
	}
	return s, err == nil, err
}

func Remaining(s Session, now time.Time) int64 {
	if Terminal(s.State) {
		return 0
	}
	if s.State == Lost || s.State == Pending {
		return max(0, s.RemainingMS)
	}
	return max(0, s.EndMS-now.UnixMilli())
}

func Frozen(s Session, now time.Time) bool {
	return s.State == Lost || s.State == Pending ||
		(!Terminal(s.State) && s.LastReportAt != nil && now.Sub(*s.LastReportAt) > OnlineWindow)
}

// Occupied supplements commercial intervals with recovered execution intervals.
// All mutation callers hold the space lock before using this check.
func Occupied(db *gorm.DB, slotID, exclude uint, start, end, now time.Time) (bool, error) {
	var n int64
	err := db.Model(&Session{}).Where("slot_id = ? AND reservation_id <> ? AND state IN ? AND start_ms < ? AND (end_ms + extra_ms) > ?", slotID, exclude,
		[]string{Running, Recovering, Lost}, end.UnixMilli(), start.UnixMilli()).Count(&n).Error
	return n > 0, err
}

// ExtensionClock only permits extensions of a connected, acknowledged execution.
// Without a real session, staging simulation retains the existing booking clock.
func ExtensionClock(db *gorm.DB, r reservation.Reservation, now time.Time) (time.Time, bool, error) {
	s, found, err := Find(db, r.ID)
	if err != nil {
		return time.Time{}, false, err
	}
	if !found {
		return r.EndTime, now.Before(r.EndTime), nil
	}
	allowed := s.State == Running && !Frozen(s, now) && now.UnixMilli() < s.EndMS
	return time.UnixMilli(s.EndMS + s.ExtraMS).UTC(), allowed && s.ExtraMS == 0, nil
}

// AddPaidTime attaches a Checkout-confirmed extension once, in its transaction.
// The controller must receive a new command before this time becomes executable.
func AddPaidTime(db *gorm.DB, reservationID uint, minutes int) error {
	s, found, err := Find(db, reservationID)
	if err != nil || !found {
		return err
	}
	if Terminal(s.State) {
		return errors.New("equipment session is terminal")
	}
	s.ExtraMS += int64(minutes) * 60000
	s.PaidMinutes += minutes
	s.Version++
	return db.Save(&s).Error
}
