// Package controllers implements outbound HTTPS polling for one controller per space.
package controllers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized controller")
var ErrStale = errors.New("stale controller command")

const OnlineWindow = 12 * time.Second

type Device struct {
	BootID        string     `json:"-"`
	ID            string     `gorm:"primaryKey;size:36" json:"id"`
	SlotID        uint       `gorm:"uniqueIndex;not null" json:"slot_id"`
	TokenHash     string     `gorm:"uniqueIndex;not null" json:"-"`
	Enabled       bool       `json:"enabled"`
	CommandID     string     `json:"-"`
	Revision      uint64     `json:"-"`
	ReservationID uint       `json:"-"`
	EndMS         int64      `json:"-"`
	AckCommandID  string     `json:"-"`
	AckOn         bool       `json:"-"`
	AckAt         *time.Time `json:"-"`
	LastSeenAt    *time.Time `json:"-"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
type Service struct {
	DB  *gorm.DB
	Now func() time.Time
}

func New(db *gorm.DB) *Service { return &Service{db, time.Now} }
func hash(token string) string { d := sha256.Sum256([]byte(token)); return hex.EncodeToString(d[:]) }
func (s *Service) db(ctx context.Context) *gorm.DB {
	return s.DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}

// Provision is intentionally a local administration operation, never a public HTTP endpoint.
func (s *Service) Provision(ctx context.Context, publicID string) (Device, string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return Device{}, "", err
	}
	token := hex.EncodeToString(secret)
	var d Device
	err := s.db(ctx).Transaction(func(tx *gorm.DB) error {
		var sl slot.Slot
		if err := tx.Where("public_id = ?", publicID).First(&sl).Error; err != nil {
			return err
		}
		if err := tx.Model(&sl).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		err := tx.Where("slot_id = ?", sl.ID).First(&d).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if d.ID == "" {
			d.ID = uuid.NewString()
			d.SlotID = sl.ID
		}
		d.BootID = ""
		d.TokenHash = hash(token)
		d.Enabled = true
		d.CommandID = uuid.NewString()
		d.Revision++
		d.ReservationID = 0
		d.EndMS = 0
		d.AckCommandID = ""
		d.AckOn = false
		d.AckAt = nil
		d.LastSeenAt = nil
		return tx.Save(&d).Error
	})
	return d, token, err
}

type Command struct {
	RequestID string `json:"request_id"`
	CommandID string `json:"command_id"`
	Revision  uint64 `json:"revision"`
	On        bool   `json:"on"`
	ServerMS  int64  `json:"server_ms"`
	EndMS     int64  `json:"end_ms"`
	PollMS    int    `json:"poll_ms"`
}

func desired(tx *gorm.DB, sl slot.Slot, enabled bool, now time.Time) (uint, int64, error) {
	if !enabled || !sl.IsAvailable {
		return 0, 0, nil
	}
	var rs []reservation.Reservation
	err := tx.Where("slot_id = ? AND parent_reservation_id IS NULL AND status = ? AND qr_verified_at IS NOT NULL AND start_time <= ? AND end_time > ?", sl.ID, "confirmed", now, now).Limit(2).Find(&rs).Error
	if err != nil {
		return 0, 0, err
	}
	if len(rs) != 1 {
		return 0, 0, nil
	} // Conflicting data fails closed.
	return rs[0].ID, rs[0].EndTime.UnixMilli(), nil
}
func syncCommand(tx *gorm.DB, d *Device, sl slot.Slot, now time.Time) error {
	id, end, err := desired(tx, sl, d.Enabled, now)
	if err != nil {
		return err
	}
	if d.CommandID == "" || d.ReservationID != id || d.EndMS != end {
		d.CommandID = uuid.NewString()
		d.Revision++
		d.ReservationID = id
		d.EndMS = end
		d.AckOn = false
		d.AckCommandID = ""
		d.AckAt = nil
	}
	return nil
}

// Slot first, device second: same slot lock used by booking/payment writers.
func (s *Service) withDevice(ctx context.Context, token string, fn func(*gorm.DB, *Device, slot.Slot) error) error {
	if len(token) != 64 {
		return ErrUnauthorized
	}
	db := s.db(ctx)
	var known Device
	if err := db.Where("token_hash = ?", hash(token)).First(&known).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUnauthorized
		}
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&slot.Slot{}).Where("id = ?", known.SlotID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		var sl slot.Slot
		if err := tx.First(&sl, known.SlotID).Error; err != nil {
			return err
		}
		if err := tx.Model(&Device{}).Where("id = ?", known.ID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		var d Device
		if err := tx.Where("id = ? AND token_hash = ?", known.ID, hash(token)).First(&d).Error; err != nil {
			return ErrUnauthorized
		}
		if !d.Enabled {
			return ErrUnauthorized
		}
		return fn(tx, &d, sl)
	})
}
func (s *Service) Poll(ctx context.Context, token, requestID, bootID string) (Command, error) {
	var c Command
	err := s.withDevice(ctx, token, func(tx *gorm.DB, d *Device, sl slot.Slot) error {
		now := s.Now()
		if d.BootID != bootID {
			d.BootID = bootID
			d.CommandID = ""
		}
		if err := syncCommand(tx, d, sl, now); err != nil {
			return err
		}
		d.LastSeenAt = &now
		if err := tx.Save(d).Error; err != nil {
			return err
		}
		c = Command{requestID, d.CommandID, d.Revision, d.ReservationID != 0, now.UnixMilli(), d.EndMS, 2500}
		return nil
	})
	return c, err
}
func (s *Service) Ack(ctx context.Context, token, commandID string, on bool) error {
	return s.withDevice(ctx, token, func(tx *gorm.DB, d *Device, sl slot.Slot) error {
		now := s.Now()
		if err := syncCommand(tx, d, sl, now); err != nil {
			return err
		}
		if d.CommandID != commandID || on != (d.ReservationID != 0) {
			return ErrStale
		}
		d.AckCommandID = commandID
		d.AckOn = on
		d.AckAt = &now
		d.LastSeenAt = &now
		return tx.Save(d).Error
	})
}

// Activation reports a GPIO acknowledgement, not proof of mains voltage or motor operation.
func Activation(db *gorm.DB, r reservation.Reservation, now time.Time) (string, error) {
	if r.QRVerifiedAt == nil {
		return "not_started", nil
	}
	var d Device
	err := db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Where("slot_id = ?", r.SlotID).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "simulated", nil
	}
	if err != nil {
		return "", err
	}
	if r.Status != "confirmed" || !now.Before(r.EndTime) {
		return "ended", nil
	}
	if !d.Enabled {
		return "controller_unavailable", nil
	}
	if d.LastSeenAt == nil || now.Sub(*d.LastSeenAt) > OnlineWindow {
		return "controller_offline", nil
	}
	if d.ReservationID == r.ID && d.EndMS == r.EndTime.UnixMilli() && d.AckOn && d.AckAt != nil && now.Sub(*d.AckAt) <= OnlineWindow && d.AckCommandID == d.CommandID {
		return "active", nil
	}
	return "awaiting_controller", nil
}
