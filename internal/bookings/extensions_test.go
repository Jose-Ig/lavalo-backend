package bookings

import (
	"context"
	"errors"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	"gorm.io/gorm"
	"testing"
	"time"
)

func activeFixture(t *testing.T) (*Service, *reservation.Reservation) {
	s, r, _ := confirmedFixture(t)
	now := r.StartTime.Add(time.Minute)
	s.Now = func() time.Time { return now }
	access, err := s.VerifySlotQR(context.Background(), ConfirmationCode(*r), testSlotQR(t, s), r.StartTime.In(Location).Format("2006-01-02"))
	if err != nil || access.ActivationStatus != "simulated" {
		t.Fatal("QR did not start simulation", access, err)
	}
	r.QRVerifiedAt = access.QRVerifiedAt
	return s, r
}
func TestExtensionHoldBlocksCompetitorsAndCancellationReleasesIt(t *testing.T) {
	s, r := activeFixture(t)
	ctx := context.Background()
	code := ConfirmationCode(*r)
	day := r.StartTime.In(Location).Format("2006-01-02")
	options, err := s.ExtensionOptions(ctx, code, day)
	if err != nil || len(options) != 3 || options[0].AmountCents != 125000 {
		t.Fatal(options, err)
	}
	child, token, err := s.CreateExtension(ctx, code, day, 30)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentReservationID == nil || *child.ParentReservationID != r.ID || child.SlotID != r.SlotID || !child.StartTime.Equal(r.EndTime) || child.Status != "pending" {
		t.Fatal("bad hold", child)
	}
	var parent reservation.Reservation
	s.DB.First(&parent, r.ID)
	if !parent.EndTime.Equal(r.EndTime) {
		t.Fatal("unpaid minutes added")
	}
	if _, _, err = s.CreateExtension(ctx, code, day, 15); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate extension allowed", err)
	}
	free, err := s.Available(ctx, r.EndTime, 15)
	if err != nil || len(free) != 0 {
		t.Fatal("hold not blocking", free, err)
	}
	if err = s.Cancel(ctx, child.ID, token); err != nil {
		t.Fatal(err)
	}
	options, err = s.ExtensionOptions(ctx, code, day)
	if err != nil || len(options) != 3 {
		t.Fatal("cancel did not free hold", options, err)
	}
}
func TestExtensionHonorsNextBookingClosingAndExpiry(t *testing.T) {
	s, r := activeFixture(t)
	ctx := context.Background()
	code := ConfirmationCode(*r)
	day := r.StartTime.In(Location).Format("2006-01-02")
	next, _, err := s.Create(ctx, Input{SlotID: r.SlotID, StartTime: r.EndTime.Add(30 * time.Minute), Duration: 15, Name: r.Name, Phone: r.Phone})
	if err != nil {
		t.Fatal(err)
	}
	options, err := s.ExtensionOptions(ctx, code, day)
	if err != nil || len(options) != 2 {
		t.Fatal("next booking ignored", options, err)
	}
	if _, _, err = s.CreateExtension(ctx, code, day, 60); !errors.Is(err, ErrConflict) {
		t.Fatal("overlap accepted", err)
	}
	if err = s.DB.Model(next).Update("status", "cancelled").Error; err != nil {
		t.Fatal(err)
	}
	child, _, err := s.CreateExtension(ctx, code, day, 15)
	if err != nil {
		t.Fatal(err)
	}
	now := child.ExpiresAt.Add(time.Second)
	s.Now = func() time.Time { return now }
	options, err = s.ExtensionOptions(ctx, code, day)
	if err != nil || len(options) != 3 {
		t.Fatal("expired hold blocks", options, err)
	}
	closing := time.Date(r.StartTime.In(Location).Year(), r.StartTime.In(Location).Month(), r.StartTime.In(Location).Day(), 20, 45, 0, 0, Location)
	s.DB.Model(r).Update("end_time", closing)
	options, err = s.ExtensionOptions(ctx, code, day)
	if err != nil || len(options) != 1 || options[0].Minutes != 15 {
		t.Fatal("closing ignored", options, err)
	}
}
func TestExtensionRequiresLiveVerifiedParentAndValidDuration(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	ctx := context.Background()
	code := ConfirmationCode(*r)
	day := r.StartTime.In(Location).Format("2006-01-02")
	if _, _, err := s.CreateExtension(ctx, code, day, 15); !errors.Is(err, ErrConflict) {
		t.Fatal("unverified accepted", err)
	}
	now := r.StartTime
	s.Now = func() time.Time { return now }
	s.DB.Model(r).Update("qr_verified_at", now)
	if _, _, err := s.CreateExtension(ctx, code, day, 45); !errors.Is(err, ErrInvalid) {
		t.Fatal("bad duration accepted", err)
	}
	if _, _, err := s.CreateExtension(ctx, "123456-010100", day, 15); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("wrong code accepted", err)
	}
	now = r.EndTime
	if _, _, err := s.CreateExtension(ctx, code, day, 15); !errors.Is(err, ErrConflict) {
		t.Fatal("ended accepted", err)
	}
}
func TestConcurrentExtensionHoldsHaveOneWinner(t *testing.T) {
	s, r := activeFixture(t)
	code := ConfirmationCode(*r)
	day := r.StartTime.In(Location).Format("2006-01-02")
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, _, err := s.CreateExtension(context.Background(), code, day, 15); results <- err }()
	}
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			wins++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("duplicate holds", wins, conflicts)
	}
}
