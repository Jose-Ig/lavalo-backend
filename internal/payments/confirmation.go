package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// VerifySignature binds the URL payment ID and request ID using MP's documented HMAC manifest.
// No timestamp cutoff: delayed provider retries remain valid and processing is idempotent.
func VerifySignature(r *http.Request, secret string) bool {
	id := r.URL.Query().Get("data.id")
	requestID := r.Header.Get("x-request-id")
	if secret == "" || id == "" || requestID == "" {
		return false
	}
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
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
	// Never use the body data.id or resource URL; only fetch the signed numeric URL ID.
	p, err := s.Gateway.GetPayment(c.Request.Context(), c.Query("data.id"))
	if err == nil && strconv.FormatInt(p.ID, 10) != c.Query("data.id") {
		err = ErrMismatch
	}
	if err == nil {
		err = s.Apply(c.Request.Context(), p)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.Status(200)
		return
	} // Another integration's external reference.
	if err != nil {
		failure(c, err)
		return
	}
	c.Status(200)
}

// Apply serializes against booking creation via the space row, then against cancellation via the reservation row.
func (s *Service) Apply(ctx context.Context, p ProviderPayment) error {
	var known Checkout
	if err := s.DB.WithContext(ctx).Where("reference = ?", p.Reference).First(&known).Error; err != nil {
		return err
	}
	amount, err := moneyCents(p.Amount)
	if err != nil || p.ID <= 0 || amount != known.AmountCents || p.Currency != known.Currency || p.CollectorID != s.Config.CollectorID || p.LiveMode != s.Config.LiveMode || p.UpdatedAt.IsZero() {
		return ErrMismatch
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r reservation.Reservation
		if err := tx.First(&r, known.ReservationID).Error; err != nil {
			return err
		}
		if err := tx.Model(&slot.Slot{}).Where("id = ?", r.SlotID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		if err := tx.Model(&reservation.Reservation{}).Where("id = ?", r.ID).Update("updated_at", s.Now()).Error; err != nil {
			return err
		}
		if err := tx.First(&r, r.ID).Error; err != nil {
			return err
		}
		var checkout Checkout
		if err := tx.First(&checkout, known.ID).Error; err != nil {
			return err
		}
		id := strconv.FormatInt(p.ID, 10)
		var receipt Receipt
		err := tx.Where("provider_id = ?", id).First(&receipt).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if receipt.CheckoutID != checkout.ID {
				return ErrMismatch
			}
			if !p.UpdatedAt.After(receipt.ProviderUpdatedAt) {
				return nil
			}
		} else {
			receipt = Receipt{CheckoutID: checkout.ID, ProviderID: id}
		}
		receipt.Status = p.Status
		receipt.StatusDetail = p.Detail
		receipt.ProviderUpdatedAt = p.UpdatedAt
		// A partial refund must also block unqualified confirmation.
		if p.Refunded != "" {
			refunded, e := moneyCents(p.Refunded)
			if e != nil {
				return e
			}
			if refunded > 0 && p.Status == "approved" {
				receipt.Status = "partially_refunded"
			}
		}
		if err := tx.Save(&receipt).Error; err != nil {
			return err
		}
		var receipts []Receipt
		if err := tx.Where("checkout_id = ?", checkout.ID).Find(&receipts).Error; err != nil {
			return err
		}
		approved, refunded, failed, pending := 0, 0, 0, 0
		for _, v := range receipts {
			switch v.Status {
			case "approved":
				approved++
			case "refunded", "charged_back", "partially_refunded":
				refunded++
			case "rejected", "cancelled":
				failed++
			default:
				pending++
			}
		}
		state, issue := "pending", ""
		switch {
		case checkout.State == "review":
			state = "review"
			issue = checkout.Issue
		case refunded > 0:
			state = "refunded"
			if approved > 0 {
				state = "review"
				issue = "multiple_payments"
			}
			if r.Status == "confirmed" {
				r.Status = "cancelled"
			} else if r.Status == "completed" {
				state = "review"
				issue = "refund_after_service"
			}
		case approved > 1:
			state = "review"
			issue = "multiple_payments"
		case approved == 1:
			if r.Status == "confirmed" || r.Status == "completed" {
				state = "completed"
				break
			}
			if r.Status != "pending" || r.ExpiresAt == nil || !s.Now().Before(*r.ExpiresAt) {
				state = "review"
				issue = "late_payment"
				break
			}
			var overlaps int64
			err := tx.Model(&reservation.Reservation{}).Where("id <> ? AND slot_id = ? AND start_time < ? AND end_time > ?", r.ID, r.SlotID, r.EndTime, r.StartTime).Where("status = ? OR (status = ? AND (expires_at IS NULL OR expires_at > ?))", "confirmed", "pending", s.Now()).Count(&overlaps).Error
			if err != nil {
				return err
			}
			if overlaps > 0 {
				state = "review"
				issue = "slot_unavailable"
				break
			}
			r.Status = "confirmed"
			r.ExpiresAt = nil
			state = "completed"
		case failed > 0 && pending == 0:
			state = "failed"
		}
		if err := tx.Model(&r).Updates(map[string]any{"status": r.Status, "expires_at": r.ExpiresAt}).Error; err != nil {
			return err
		}
		return tx.Model(&checkout).Updates(map[string]any{"state": state, "issue": issue}).Error
	})
}

func (s *Service) Reconcile(ctx context.Context, c Checkout) error {
	if !s.Config.Ready() {
		return ErrUnavailable
	}
	payments, err := s.Gateway.SearchPayments(ctx, c.Reference)
	if err != nil {
		return err
	}
	for _, p := range payments {
		// Search is authenticated but check the reference again before processing.
		if p.Reference != c.Reference {
			return ErrMismatch
		}
		if err = s.Apply(ctx, p); err != nil {
			return err
		}
	}
	return nil
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
		if err := s.DB.WithContext(ctx).Order("COALESCE(reconciled_at, created_at) ASC").Limit(20).Find(&batch).Error; err != nil {
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
