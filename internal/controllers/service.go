// Package controllers implements outbound HTTPS polling for one controller per space.
package controllers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Jose-Ig/lavalo-backend/internal/operations"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized controller")
var ErrStale = errors.New("stale controller command")

const OnlineWindow = operations.OnlineWindow

var ErrBootstrap = errors.New("controller bootstrap required")

type Device struct {
	SessionID     string     `json:"-"`
	Protocol      int        `json:"-"`
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
		d.Protocol = 0
		// Rotating credentials must not discard a recoverable session.
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
	RequestID   string `json:"request_id"`
	BootID      string `json:"boot_id"`
	Protocol    int    `json:"protocol"`
	CommandID   string `json:"command_id"`
	Revision    uint64 `json:"revision"`
	On          bool   `json:"on"`
	ServerMS    int64  `json:"server_ms"`
	EndMS       int64  `json:"end_ms"`
	RemainingMS int64  `json:"remaining_ms"`
	SessionID   string `json:"session_id,omitempty"`
	ExecutionID string `json:"execution_id,omitempty"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	PollMS      int    `json:"poll_ms"`
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
