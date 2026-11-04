package payments

import (
	"context"
	"github.com/Jose-Ig/lavalo-backend/internal/bookings"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"testing"
	"time"
)

func TestThreeMinutePaymentAndPaidExtension(t *testing.T) {
	s, _, _, _ := fixture(t)
	ctx := context.Background()
	r, token, err := s.Bookings.Create(ctx, bookings.Input{SlotID: 1, StartTime: s.Now().Add(65 * time.Minute), Duration: 3, Name: "Short Test", Phone: "1234567890"})
	// The fixture's existing hour-long reservation occupies this slot; cancel it first.
	if err == nil {
		t.Fatal("overlapping fixture booking accepted", r)
	}
	if err = s.DB.Model(&reservation.Reservation{}).Where("status = ?", "pending").Update("status", "cancelled").Error; err != nil {
		t.Fatal(err)
	}
	r, token, err = s.Bookings.Create(ctx, bookings.Input{SlotID: 1, StartTime: s.Now().Add(65 * time.Minute), Duration: 3, Name: "Short Test", Phone: "1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	pay := func(id uint, secret string) {
		t.Helper()
		v, e := s.Start(ctx, id, secret)
		if e != nil || v.AmountCents != 25000 {
			t.Fatal("wrong short-turn checkout", v, e)
		}
		order := approved(s, v.Checkout)
		order.Amount, order.PaidAmount = "250.00", "250.00"
		if e = s.Apply(ctx, order); e != nil {
			t.Fatal(e)
		}
	}
	pay(r.ID, token)
	if err = s.DB.First(r, r.ID).Error; err != nil {
		t.Fatal(err)
	}
	if r.Status != "confirmed" || r.EndTime.Sub(r.StartTime) != 3*time.Minute {
		t.Fatal("incorrect paid short turn", r)
	}
	now := r.StartTime.Add(time.Second)
	s.Now = func() time.Time { return now }
	s.Bookings.Now = s.Now
	var assigned slot.Slot
	if err = s.DB.First(&assigned, r.SlotID).Error; err != nil {
		t.Fatal(err)
	}
	code, day := bookings.ConfirmationCode(*r), r.StartTime.In(bookings.Location).Format("2006-01-02")
	if _, err = s.Bookings.VerifySlotQR(ctx, code, "uash://slot/"+assigned.PublicID, day); err != nil {
		t.Fatal(err)
	}
	child, secret, err := s.Bookings.CreateExtension(ctx, code, day, 3)
	if err != nil {
		t.Fatal(err)
	}
	end := r.EndTime
	if !child.StartTime.Equal(end) || child.EndTime.Sub(end) != 3*time.Minute {
		t.Fatal("extension must be contiguous")
	}
	pay(child.ID, secret)
	if err = s.DB.First(r, r.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !r.EndTime.Equal(end.Add(3*time.Minute)) || r.DurationMinutes != 6 || bookings.ConfirmationCode(*r) != code {
		t.Fatal("wrong extended countdown", r)
	}
}
