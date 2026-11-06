package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Jose-Ig/lavalo-backend/internal/operations"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Jose-Ig/lavalo-backend/internal/bookings"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// VerifySignature binds the URL order ID and request ID using MP's documented HMAC manifest.
// No timestamp cutoff: delayed provider retries remain valid and processing is idempotent.
func VerifySignature(r *http.Request, secret string) bool {
	id := r.URL.Query().Get("data.id")
	requestID := r.Header.Get("x-request-id")
	if secret == "" || id == "" || requestID == "" {
		return false
	}
	if !validOrderID(id) {
		return false
	}
	var ts, signature string
	for _, part := range strings.Split(r.Header.Get("x-signature"), ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			return false
		}
		switch kv[0] {
		case "ts":
			if ts != "" {
				return false
			}
			ts = kv[1]
		case "v1":
			if signature != "" {
				return false
			}
			signature = kv[1]
		}
	}
	if _, err := strconv.ParseInt(ts, 10, 64); err != nil {
		return false
	}
	received, err := hex.DecodeString(signature)
	if err != nil || len(received) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "id:%s;request-id:%s;ts:%s;", strings.ToLower(id), requestID, ts)
	return hmac.Equal(received, mac.Sum(nil))
}

func (s *Service) webhook(c *gin.Context) {
	if !s.Config.Ready() {
		c.Status(503)
		return
	}
	if !VerifySignature(c.Request, s.Config.WebhookSecret) {
		c.Status(401)
		return
	}
	if topic := c.Query("type"); topic != "" && topic != "order" {
		c.Status(200)
		return
	}
	// Only the signed order ID is used; the redirect/body never authorizes a reservation.
	id := strings.ToUpper(c.Query("data.id"))
	o, e := s.Gateway.GetOrder(c.Request.Context(), id)
	if e == nil && o.ID != id {
		e = ErrMismatch
	}
	if e == nil {
		e = s.Apply(c.Request.Context(), o)
	}
	if errors.Is(e, gorm.ErrRecordNotFound) {
		c.Status(200)
		return
	}
	if e != nil {
		failure(c, e)
		return
	}
	c.Status(200)
}

// Apply serializes with booking creation through the space row and with cancellation through the reservation row.
func (s *Service) Apply(ctx context.Context, o Order) error {
	var known Checkout
	if e := s.DB.WithContext(ctx).Where("reference = ?", o.Reference).First(&known).Error; e != nil {
		return e
	}
	if known.API != "orders" {
		return ErrMismatch
	}
	if e := validateOrder(o, known, s.Config); e != nil {
		return e
	}
	if known.OrderID == nil {
		return ErrUnavailable
	} // Creation response not persisted yet: retry later.
	if *known.OrderID != o.ID {
		return ErrMismatch
	}
	state, issue := "pending", ""
	switch o.Status {
	case "created", "processing", "action_required":
	case "failed":
		state = "failed"
	case "canceled":
		state = "failed"
		issue = "order_canceled"
	case "processed":
		if o.Detail == "partially_refunded" {
			state = "review"
			issue = "partial_refund"
			break
		}
		if o.Detail != "accredited" {
			state = "review"
			issue = "unrecognized_order_status"
			break
		}
		paid, e := moneyCents(json.Number(o.PaidAmount))
		if e != nil || paid != known.AmountCents {
			return ErrMismatch
		}
		state = "completed"
	case "refunded", "charged_back":
		state = "refunded"
	default:
		state = "review"
		issue = "unrecognized_order_status"
	}
	// Fail closed on partial refunds/chargebacks visible in transactions even when the aggregate status lags.
	for _, p := range o.Transactions.Payments {
		if p.Status == "charged_back" || p.Status == "refunded" {
			state = "refunded"
		}
		if p.Detail == "partially_refunded" {
			state = "review"
			issue = "partial_refund"
		}
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r reservation.Reservation
		if e := tx.First(&r, known.ReservationID).Error; e != nil {
			return e
		}
		if e := tx.Model(&slot.Slot{}).Where("id = ?", r.SlotID).Update("updated_at", s.Now()).Error; e != nil {
			return e
		}
		if r.ParentReservationID != nil {
			if e := tx.Model(&reservation.Reservation{}).Where("id = ?", *r.ParentReservationID).Update("updated_at", s.Now()).Error; e != nil {
				return e
			}
		}
		if e := tx.Model(&reservation.Reservation{}).Where("id = ?", r.ID).Update("updated_at", s.Now()).Error; e != nil {
			return e
		}
		if e := tx.First(&r, r.ID).Error; e != nil {
			return e
		}
		var checkout Checkout
		if e := tx.First(&checkout, known.ID).Error; e != nil {
			return e
		}
		var receipt Receipt
		e := tx.Where("provider_id = ?", o.ID).First(&receipt).Error
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if e == nil {
			if receipt.CheckoutID != checkout.ID {
				return ErrMismatch
			}
			if !o.UpdatedAt.After(receipt.ProviderUpdatedAt) {
				return nil
			}
		} else {
			receipt = Receipt{CheckoutID: checkout.ID, ProviderID: o.ID}
		}
		receipt.Status = o.Status
		receipt.StatusDetail = o.Detail
		receipt.ProviderUpdatedAt = o.UpdatedAt
		if e := tx.Save(&receipt).Error; e != nil {
			return e
		}
		// Business reviews (late/partial/unknown) need human resolution, never a later redirect.
		if checkout.State == "review" {
			state = "review"
			issue = checkout.Issue
		}
		if r.ParentReservationID != nil && (state == "refunded" || issue == "partial_refund") && r.Status == "completed" {
			state, issue = "review", "extension_refund_after_service"
		}
		if state == "refunded" || issue == "partial_refund" {
			if r.Status == "confirmed" {
				r.Status = "cancelled"
			} else if r.Status == "completed" {
				state = "review"
				issue = "refund_after_service"
			}
		}
		if state == "completed" {
			switch r.Status {
			case "confirmed", "completed":
			case "pending":
				if r.ExpiresAt == nil || !s.Now().Before(*r.ExpiresAt) {
					state = "review"
					issue = "late_payment"
					break
				}
				var overlaps int64
				e := tx.Model(&reservation.Reservation{}).Where("id <> ? AND slot_id = ? AND start_time < ? AND end_time > ?", r.ID, r.SlotID, r.EndTime, r.StartTime).Where("status = ? OR (status = ? AND (expires_at IS NULL OR expires_at > ?))", "confirmed", "pending", s.Now()).Count(&overlaps).Error
				if e != nil {
					return e
				}
				busy, err := operations.Occupied(tx, r.SlotID, r.ID, r.StartTime, r.EndTime, s.Now())
				if err != nil {
					return err
				}
				if r.ParentReservationID != nil {
					// A child extends its own parent's execution, not a competing reservation.
					var own operations.Session
					if err := tx.Where("reservation_id = ?", *r.ParentReservationID).First(&own).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
					if own.ID != "" {
						busy, err = operations.Occupied(tx, r.SlotID, *r.ParentReservationID, r.StartTime, r.EndTime, s.Now())
						if err != nil {
							return err
						}
					}
				}
				if overlaps > 0 || busy {
					state = "review"
					issue = "slot_unavailable"
					break
				}
				if r.ParentReservationID != nil {
					var parent reservation.Reservation
					if e := tx.First(&parent, *r.ParentReservationID).Error; e != nil {
						return e
					}
					var enabledSlot slot.Slot
					if e := tx.First(&enabledSlot, r.SlotID).Error; e != nil {
						return e
					}
					effective, extensionAllowed, err := operations.ExtensionClock(tx, parent, s.Now())
					if err != nil {
						return err
					}
					matchesExecution := effective.Equal(r.StartTime)
					session, found, err := operations.Find(tx, parent.ID)
					if err != nil {
						return err
					}
					if found && !operations.Terminal(session.State) {
						// The purchase was initiated while connected; a subsequent
						// outage does not discard its newly confirmed minutes.
						extensionAllowed = session.ExtraMS == 0
						if r.ParentExecutionID != "" {
							var count int64
							if err := tx.Model(&operations.Execution{}).Where("id = ? AND session_id = ?", r.ParentExecutionID, session.ID).Count(&count).Error; err != nil {
								return err
							}
							matchesExecution = count == 1
						}
					}
					if !extensionAllowed || !matchesExecution || !enabledSlot.IsAvailable || parent.Status != "confirmed" || parent.QRVerifiedAt == nil || parent.SlotID != r.SlotID || !s.Now().Before(effective) {
						state, issue = "review", "extension_unavailable"
						break
					}
					if e := tx.Model(&parent).Updates(map[string]any{"end_time": parent.EndTime.Add(time.Duration(r.DurationMinutes) * time.Minute), "duration_minutes": parent.DurationMinutes + r.DurationMinutes}).Error; e != nil {
						return e
					}
					if e := operations.AddPaidTime(tx, parent.ID, r.DurationMinutes); e != nil {
						return e
					}
					r.Status = "completed"
				} else {
					r.Status = "confirmed"
				}
				r.ExpiresAt = nil
			default:
				state = "review"
				issue = "late_payment"
			}
		}
		if e := tx.Model(&r).Updates(map[string]any{"status": r.Status, "expires_at": r.ExpiresAt}).Error; e != nil {
			return e
		}
		if r.Status == "confirmed" {
			if e := bookings.EnsureConfirmation(tx, &r); e != nil {
				return e
			}
		}
		return tx.Model(&checkout).Updates(map[string]any{"state": state, "issue": issue}).Error
	})
}
func (s *Service) Reconcile(ctx context.Context, c Checkout) error {
	if !s.Config.Ready() {
		return ErrUnavailable
	}
	if c.API != "orders" {
		return nil
	}
	if c.SellerID != strconv.FormatInt(s.Config.CollectorID, 10) || c.LiveMode != s.Config.LiveMode {
		return ErrMismatch
	}
	if c.OrderID == nil {
		if !s.Now().Before(c.ExpiresAt) {
			return nil
		}
		return s.ensureOrder(ctx, &c)
	}
	o, e := s.Gateway.GetOrder(ctx, *c.OrderID)
	if e != nil {
		return e
	}
	if o.ID != *c.OrderID {
		return ErrMismatch
	}
	return s.Apply(ctx, o)
}

// RunReconciler repairs missed webhooks, including later refunds, without exposing private jobs over HTTP.
func (s *Service) RunReconciler(ctx context.Context) {
	if !s.Config.Ready() {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		var batch []Checkout
		if err := s.DB.WithContext(ctx).Where("api = ?", "orders").Order("COALESCE(reconciled_at, created_at) ASC").Limit(20).Find(&batch).Error; err != nil {
			log.Print("checkout reconciliation: database unavailable")
			continue
		}
		for _, c := range batch {
			if ctx.Err() != nil {
				return
			}
			call, cancel := context.WithTimeout(ctx, 20*time.Second)
			err := s.Reconcile(call, c)
			cancel()
			if err != nil {
				log.Printf("checkout reconciliation failed: checkout=%d", c.ID)
			}
			// Rotate errors too, so one broken integration cannot starve the queue.
			s.DB.WithContext(ctx).Model(&Checkout{}).Where("id = ?", c.ID).Update("reconciled_at", s.Now())
		}
	}
}
