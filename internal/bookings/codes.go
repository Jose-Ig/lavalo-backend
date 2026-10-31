package bookings

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// NotificationIntent records delivery work; it never claims that a message was sent.
// Dispatch requires a separately configured provider. No recipient/code is copied into logs or this table.
type NotificationIntent struct {
	ID            uint   `gorm:"primaryKey"`
	ReservationID uint   `gorm:"uniqueIndex:reservation_channel;not null"`
	Channel       string `gorm:"uniqueIndex:reservation_channel;not null"`
	Status        string `gorm:"not null"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

var codeAlphabet = base32.NewEncoding("23456789ABCDEFGHJKLMNPQRSTUVWXYZ").WithPadding(base32.NoPadding)

// Each reservation already has an independent 256-bit random capability (stored hashed).
// Domain-separated derivation makes its customer code stable without storing plaintext codes.
func ConfirmationCode(r reservation.Reservation) string {
	if len(r.AccessTokenHash) != 64 || r.ID == 0 {
		return ""
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("uash:confirmation:v1:%d:%s", r.ID, r.AccessTokenHash)))
	raw := codeAlphabet.EncodeToString(digest[:10]) // 80 bits, 16 printable characters.
	return "UASH-" + raw[:4] + "-" + raw[4:8] + "-" + raw[8:12] + "-" + raw[12:]
}
func normalizeCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", "", "\n", "", "\r", "").Replace(strings.TrimSpace(code)))
}
func codeHash(code string) string {
	sum := sha256.Sum256([]byte(normalizeCode(code)))
	return hex.EncodeToString(sum[:])
}
func EnsureConfirmation(tx *gorm.DB, r *reservation.Reservation) error {
	if r.Status != "confirmed" && r.Status != "completed" {
		return nil
	}
	code := ConfirmationCode(*r)
	if code == "" {
		return ErrInvalid
	}
	hash := codeHash(code)
	if r.CodeHash == nil {
		if err := tx.Model(r).Where("code_hash IS NULL").Update("code_hash", hash).Error; err != nil {
			return err
		}
		r.CodeHash = &hash
	}
	channels := []string{}
	if r.Phone != "" {
		channels = append(channels, "sms")
	}
	if r.WhatsAppOptIn && r.Phone != "" {
		channels = append(channels, "whatsapp")
	}
	for _, channel := range channels {
		intent := NotificationIntent{ReservationID: r.ID, Channel: channel, Status: "pending_configuration"}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "reservation_id"}, {Name: "channel"}}, DoNothing: true}).Create(&intent).Error; err != nil {
			return err
		}
	}
	return nil
}

type ReservationAccess struct {
	ID               uint                          `json:"id"`
	Code             string                        `json:"code"`
	SlotID           uint                          `json:"slot_id"`
	SlotLabel        string                        `json:"slot_label"`
	StartTime        time.Time                     `json:"start_time"`
	EndTime          time.Time                     `json:"end_time"`
	Duration         int                           `json:"duration_minutes"`
	Status           reservation.ReservationStatus `json:"status"`
	CanVerifyQR      bool                          `json:"can_verify_qr"`
	ActivationReason string                        `json:"activation_reason"`
	QRVerifiedAt     *time.Time                    `json:"qr_verified_at,omitempty"`
	ActivationStatus string                        `json:"activation_status"`
}

func (s *Service) findByCode(db *gorm.DB, code string) (reservation.Reservation, error) {
	var r reservation.Reservation
	normalized := normalizeCode(code)
	if len(normalized) != 20 || !strings.HasPrefix(normalized, "UASH") {
		return r, gorm.ErrRecordNotFound
	}
	err := db.Where("code_hash = ?", codeHash(code)).First(&r).Error
	return r, err
}
func (s *Service) access(db *gorm.DB, r reservation.Reservation) (ReservationAccess, error) {
	var sl slot.Slot
	if err := db.First(&sl, r.SlotID).Error; err != nil {
		return ReservationAccess{}, err
	}
	reason := "ready"
	switch {
	case r.Status != "confirmed":
		reason = "not_confirmed"
	case s.Now().Before(r.StartTime):
		reason = "too_early"
	case !s.Now().Before(r.EndTime):
		reason = "expired"
	case !sl.IsAvailable:
		reason = "slot_unavailable"
	}
	activation := "not_started"
	if r.QRVerifiedAt != nil {
		activation = "awaiting_controller"
	}
	return ReservationAccess{ID: r.ID, Code: ConfirmationCode(r), SlotID: r.SlotID, SlotLabel: sl.Label, StartTime: r.StartTime, EndTime: r.EndTime, Duration: r.DurationMinutes, Status: r.Status, CanVerifyQR: reason == "ready", ActivationReason: reason, QRVerifiedAt: r.QRVerifiedAt, ActivationStatus: activation}, nil
}
func (s *Service) LookupCode(ctx context.Context, code string) (ReservationAccess, error) {
	db := s.DB.WithContext(ctx)
	r, err := s.findByCode(db, code)
	if err != nil {
		return ReservationAccess{}, err
	}
	return s.access(db, r)
}
func parseSlotQR(raw string) (uint, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "uash" || u.Host != "slot" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return 0, ErrInvalid
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(u.Path, "/"), 10, 32)
	if err != nil || id == 0 {
		return 0, ErrInvalid
	}
	return uint(id), nil
}
func (s *Service) VerifySlotQR(ctx context.Context, code, qr string) (ReservationAccess, error) {
	slotID, err := parseSlotQR(qr)
	if err != nil {
		return ReservationAccess{}, err
	}
	var result ReservationAccess
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := s.findByCode(tx, code)
		if err != nil {
			return err
		}
		if r.SlotID != slotID {
			return ErrConflict
		}
		if err = tx.Model(&slot.Slot{}).Where("id = ?", r.SlotID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		if err = tx.Model(&reservation.Reservation{}).Where("id = ?", r.ID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		if err = tx.First(&r, r.ID).Error; err != nil {
			return err
		}
		result, err = s.access(tx, r)
		if err != nil {
			return err
		}
		if !result.CanVerifyQR {
			return ErrConflict
		}
		if r.QRVerifiedAt == nil {
			now := s.Now()
			r.QRVerifiedAt = &now
			if err = tx.Model(&r).Update("qr_verified_at", now).Error; err != nil {
				return err
			}
		}
		result.QRVerifiedAt = r.QRVerifiedAt
		result.ActivationStatus = "awaiting_controller"
		return nil
	})
	return result, err
}
