package payments

import (
	"context"
	"fmt"
	"github.com/Jose-Ig/lavalo-backend/internal/bookings"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"testing"
	"time"
)

func extensionFixture(t *testing.T) (*Service, *fakeGateway, *reservation.Reservation, *reservation.Reservation, string) {
	s, f, id, token := fixture(t)
	ctx := context.Background()
	if _, err := s.Start(ctx, id, token); err != nil {
		t.Fatal(err)
	}
	var c Checkout
	s.DB.Where("reservation_id = ?", id).First(&c)
	if err := s.Apply(ctx, approved(s, c)); err != nil {
		t.Fatal(err)
	}
	var parent reservation.Reservation
	s.DB.First(&parent, id)
	now := parent.StartTime.Add(time.Minute)
	s.Now = func() time.Time { return now }
	s.Bookings.Now = s.Now
	s.DB.Model(&parent).Update("qr_verified_at", now)
	parent.QRVerifiedAt = &now
	child, secret, err := s.Bookings.CreateExtension(ctx, bookings.ConfirmationCode(parent), parent.StartTime.In(bookings.Location).Format("2006-01-02"), 15)
	if err != nil {
		t.Fatal(err)
	}
	return s, f, &parent, child, secret
}
func TestExtensionPaymentMergesOnceAndPreservesOriginalCode(t *testing.T) {
	s, _, parent, child, token := extensionFixture(t)
	ctx := context.Background()
	code := bookings.ConfirmationCode(*parent)
	v, err := s.Start(ctx, child.ID, token)
	if err != nil || v.AmountCents != 125000 || v.ReservationStatus != "pending" {
		t.Fatal(v, err)
	}
	var got reservation.Reservation
	s.DB.First(&got, parent.ID)
	if !got.EndTime.Equal(parent.EndTime) {
		t.Fatal("extended before payment")
	}
	var c Checkout
	s.DB.Where("reservation_id = ?", child.ID).First(&c)
	order := approved(s, c)
	order.Amount = "1250.00"
	order.PaidAmount = "1250.00"
	if err = s.Apply(ctx, order); err != nil {
		t.Fatal(err)
	}
	order.UpdatedAt = order.UpdatedAt.Add(time.Second)
	if err = s.Apply(ctx, order); err != nil {
		t.Fatal(err)
	}
	s.DB.First(&got, parent.ID)
	if !got.EndTime.Equal(child.EndTime) || got.DurationMinutes != parent.DurationMinutes+15 || bookings.ConfirmationCode(got) != code || got.QRVerifiedAt == nil {
		t.Fatal("incorrect merge", got)
	}
	s.DB.First(child, child.ID)
	if child.Status != "completed" {
		t.Fatal("extension not finalized")
	}
	s.DB.First(&c, c.ID)
	view, err := s.view(ctx, c, true)
	if err != nil || view.ReservationStatus != "confirmed" || view.ConfirmationCode != "" {
		t.Fatal("wrong extension view", view, err)
	}
	// A later refund requires review and must not cancel or silently shorten the parent.
	order.Status = "refunded"
	order.UpdatedAt = order.UpdatedAt.Add(time.Second)
	if err = s.Apply(ctx, order); err != nil {
		t.Fatal(err)
	}
	s.DB.First(&got, parent.ID)
	s.DB.First(&c, c.ID)
	if got.Status != "confirmed" || !got.EndTime.Equal(child.EndTime) || c.State != "review" {
		t.Fatal("refund corrupted active service", got, c.State)
	}
}
func TestLateExtensionPaymentNeverAddsMinutes(t *testing.T) {
	s, _, parent, child, token := extensionFixture(t)
	ctx := context.Background()
	if _, err := s.Start(ctx, child.ID, token); err != nil {
		t.Fatal(err)
	}
	var c Checkout
	s.DB.Where("reservation_id = ?", child.ID).First(&c)
	now := child.ExpiresAt.Add(time.Second)
	s.Now = func() time.Time { return now }
	s.Bookings.Now = s.Now
	order := approved(s, c)
	order.Amount = fmt.Sprintf("%d.%02d", c.AmountCents/100, c.AmountCents%100)
	order.PaidAmount = order.Amount
	if err := s.Apply(ctx, order); err != nil {
		t.Fatal(err)
	}
	var got reservation.Reservation
	s.DB.First(&got, parent.ID)
	s.DB.First(&c, c.ID)
	if !got.EndTime.Equal(parent.EndTime) || c.State != "review" || c.Issue != "late_payment" {
		t.Fatal("late extension applied", got, c.State, c.Issue)
	}
}

func TestExtensionPaymentRechecksParentSlotAndConflicts(t *testing.T) {
	for _, state := range []string{"cancelled_parent", "disabled_slot", "overlap", "wrong_amount"} {
		t.Run(state, func(t *testing.T) {
			s, _, parent, child, token := extensionFixture(t)
			ctx := context.Background()
			if _, err := s.Start(ctx, child.ID, token); err != nil {
				t.Fatal(err)
			}
			var c Checkout
			s.DB.Where("reservation_id = ?", child.ID).First(&c)
			order := approved(s, c)
			order.Amount = "1250.00"
			order.PaidAmount = "1250.00"
			switch state {
			case "cancelled_parent":
				s.DB.Model(parent).Update("status", "cancelled")
			case "disabled_slot":
				s.DB.Table("slots").Where("id = ?", parent.SlotID).Update("is_available", false)
			case "overlap":
				s.DB.Create(&reservation.Reservation{SlotID: parent.SlotID, StartTime: child.StartTime, EndTime: child.EndTime, Status: "confirmed"})
			case "wrong_amount":
				order.PaidAmount = "1.00"
			}
			err := s.Apply(ctx, order)
			if state == "wrong_amount" {
				if err == nil {
					t.Fatal("wrong amount accepted")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var got reservation.Reservation
			s.DB.First(&got, parent.ID)
			s.DB.First(&c, c.ID)
			if !got.EndTime.Equal(parent.EndTime) || c.State == "completed" {
				t.Fatal("invalid extension added", got, c.State)
			}
		})
	}
}
func TestPaidExtensionAllowsAnotherContiguousPurchase(t *testing.T) {
	s, _, parent, child, token := extensionFixture(t)
	ctx := context.Background()
	if _, err := s.Start(ctx, child.ID, token); err != nil {
		t.Fatal(err)
	}
	var c Checkout
	s.DB.Where("reservation_id = ?", child.ID).First(&c)
	order := approved(s, c)
	order.Amount = "1250.00"
	order.PaidAmount = "1250.00"
	if err := s.Apply(ctx, order); err != nil {
		t.Fatal(err)
	}
	var got reservation.Reservation
	s.DB.First(&got, parent.ID)
	second, _, err := s.Bookings.CreateExtension(ctx, bookings.ConfirmationCode(got), got.StartTime.In(bookings.Location).Format("2006-01-02"), 30)
	if err != nil || !second.StartTime.Equal(child.EndTime) || second.SlotID != parent.SlotID {
		t.Fatal("second extension not contiguous", second, err)
	}
}
