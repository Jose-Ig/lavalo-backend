package bookings

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/google/uuid"
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
	if r.CodeVersion == 2 {
		digest := sha256.Sum256([]byte(fmt.Sprintf("uash:confirmation:v2:%d:%s:%d", r.ID, r.AccessTokenHash, r.CodeNonce)))
		return fmt.Sprintf("%06d", binary.BigEndian.Uint64(digest[:8])%1000000)
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

	// Serialize concurrent confirmation views before allocating a code.
	if err := tx.Model(r).Where("id = ?", r.ID).Update("updated_at", time.Now()).Error; err != nil {
		return err
	}
	if err := tx.First(r, r.ID).Error; err != nil {
		return err
	}
	if r.CodeHash == nil {
		r.CodeVersion = 2
		day := r.StartTime.In(Location).Format("2006-01-02")
		allocated := false
		for nonce := 0; nonce < 1000; nonce++ {
			r.CodeNonce = nonce
			code := ConfirmationCode(*r)
			if code == "" {
				return ErrInvalid
			}
			hash := dailyCodeHash(day, code)
			var count int64
			if err := tx.Model(&reservation.Reservation{}).Unscoped().Where("code_hash = ?", hash).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				continue
			}
			// The unique index also rejects a concurrent collision. Its transaction rolls back for retry.
			result := tx.Model(r).Where("code_hash IS NULL").Updates(map[string]any{"code_hash": hash, "code_version": 2, "code_nonce": nonce})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrConflict
			}
			r.CodeHash = &hash
			allocated = true
			break
		}
		if !allocated {
			return ErrConflict
		}
	}
	channels := []string{}
	if r.Phone != "" {
		channels = append(channels, "whatsapp")
	}
	if strings.TrimSpace(r.Email) != "" {
		channels = append(channels, "email")
	}
	// Keep historical records, but never leave unsent SMS queued for a future dispatcher.
	if err := tx.Model(&NotificationIntent{}).Where("reservation_id = ? AND channel = ? AND status = ?", r.ID, "sms", "pending_configuration").Update("status", "cancelled_channel_disabled").Error; err != nil {
		return err
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
	CanReschedule     bool                          `json:"can_reschedule"`
	ReservationNumber string                        `json:"reservation_number"`
	ReservationDate   string                        `json:"reservation_date"`
	ID                uint                          `json:"id"`
	Code              string                        `json:"code"`
	SlotID            uint                          `json:"slot_id"`
	SlotLabel         string                        `json:"slot_label"`
	StartTime         time.Time                     `json:"start_time"`
	EndTime           time.Time                     `json:"end_time"`
	Duration          int                           `json:"duration_minutes"`
	Status            reservation.ReservationStatus `json:"status"`
	CanVerifyQR       bool                          `json:"can_verify_qr"`
	ActivationReason  string                        `json:"activation_reason"`
	QRVerifiedAt      *time.Time                    `json:"qr_verified_at,omitempty"`
	ActivationStatus  string                        `json:"activation_status"`
}

func dailyCodeHash(day, code string) string {
	return codeHash("uash:daily:v2:" + day + ":" + normalizeCode(code))
}
func (s *Service) lookupDay(dates []string) (string, error) {
	day := s.Now().In(Location).Format("2006-01-02")
	if len(dates) > 0 && dates[0] != "" {
		day = dates[0]
	}
	parsed, err := time.ParseInLocation("2006-01-02", day, Location)
	if err != nil || parsed.Format("2006-01-02") != day {
		return "", gorm.ErrRecordNotFound
	}
	return day, nil
}
func (s *Service) findByCode(db *gorm.DB, code, day string) (reservation.Reservation, error) {
	var r reservation.Reservation
	normalized := normalizeCode(code)
	if len(normalized) == 6 {
		for _, c := range normalized {
			if c < '0' || c > '9' {
				return r, gorm.ErrRecordNotFound
			}
		}
		err := db.Where("code_hash = ? AND code_version = ?", dailyCodeHash(day, normalized), 2).First(&r).Error
		if err == nil && r.StartTime.In(Location).Format("2006-01-02") != day {
			return r, gorm.ErrRecordNotFound
		}
		return r, err
	}
	// Existing long codes continue to work without being changed or reissued.
	if len(normalized) != 20 || !strings.HasPrefix(normalized, "UASH") {
		return r, gorm.ErrRecordNotFound
	}
	err := db.Where("code_hash = ? AND code_version = ?", codeHash(code), 0).First(&r).Error
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
	return ReservationAccess{CanReschedule: s.canReschedule(r), ReservationNumber: r.Number(), ReservationDate: r.StartTime.In(Location).Format("2006-01-02"), ID: r.ID, Code: ConfirmationCode(r), SlotID: r.SlotID, SlotLabel: sl.Label, StartTime: r.StartTime, EndTime: r.EndTime, Duration: r.DurationMinutes, Status: r.Status, CanVerifyQR: reason == "ready", ActivationReason: reason, QRVerifiedAt: r.QRVerifiedAt, ActivationStatus: activation}, nil
}
func (s *Service) LookupCode(ctx context.Context, code string, dates ...string) (ReservationAccess, error) {
	db := s.DB.WithContext(ctx)
	day, err := s.lookupDay(dates)
	if err != nil {
		return ReservationAccess{}, err
	}
	r, err := s.findByCode(db, code, day)
	if err != nil {
		return ReservationAccess{}, err
	}
	return s.access(db, r)
}
func parseSlotQR(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "uash" || u.Host != "slot" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", ErrInvalid
	}
	publicID := strings.TrimPrefix(u.Path, "/")
	id, err := uuid.Parse(publicID)
	if err != nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || id.String() != publicID || u.EscapedPath() != u.Path {
		return "", ErrInvalid
	}
	return publicID, nil
}
func (s *Service) VerifySlotQR(ctx context.Context, code, qr string, dates ...string) (ReservationAccess, error) {
	publicID, err := parseSlotQR(qr)
	if err != nil {
		return ReservationAccess{}, err
	}
	day, err := s.lookupDay(dates)
	if err != nil {
		return ReservationAccess{}, err
	}
	var result ReservationAccess
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := s.findByCode(tx, code, day)
		if err != nil {
			return err
		}
		assignedSlotID := r.SlotID
		if err = tx.Model(&slot.Slot{}).Where("id = ?", r.SlotID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		if err = tx.Model(&reservation.Reservation{}).Where("id = ?", r.ID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		if err = tx.First(&r, r.ID).Error; err != nil {
			return err
		}
		if r.SlotID != assignedSlotID {
			return ErrConflict
		}
		var assignedSlot slot.Slot
		if err := tx.First(&assignedSlot, r.SlotID).Error; err != nil {
			return err
		}
		if assignedSlot.PublicID != publicID {
			return ErrConflict
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
