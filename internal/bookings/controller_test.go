package bookings

import (
	"context"
	"github.com/Jose-Ig/lavalo-backend/internal/controllers"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestQRRequiresControllerAckBeforeActive(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	ctx := context.Background()
	cs := controllers.New(s.DB)
	cs.Now = s.Now
	var sl slot.Slot
	s.DB.First(&sl, r.SlotID)
	_, token, err := cs.Provision(ctx, sl.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	boot := uuid.NewString()
	if _, err = cs.Bootstrap(ctx, token, controllers.BootstrapInput{RequestID: uuid.NewString(), BootID: boot, Protocol: 2, Ready: true}); err != nil {
		t.Fatal(err)
	}
	off, err := cs.Poll(ctx, token, uuid.NewString(), boot)
	if err != nil || off.On {
		t.Fatal(off, err)
	}
	now := r.StartTime
	s.Now = func() time.Time { return now }
	cs.Now = s.Now
	if _, err = cs.Poll(ctx, token, uuid.NewString(), boot); err != nil {
		t.Fatal(err)
	}
	scanned, err := s.VerifySlotQR(ctx, ConfirmationCode(*r), testSlotQR(t, s), r.StartTime.In(Location).Format("2006-01-02"))
	if err != nil || scanned.ActivationStatus != "awaiting_controller" {
		t.Fatal(scanned, err)
	}
	command, err := cs.Poll(ctx, token, uuid.NewString(), boot)
	if err != nil || !command.On {
		t.Fatal(command, err)
	}
	if err = cs.AckForBoot(ctx, token, boot, command.CommandID, true, &controllers.Checkpoint{SessionID: command.SessionID, ExecutionID: command.ExecutionID, Revision: command.Revision, Sequence: 1, RemainingMS: command.EndMS - s.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupCode(ctx, ConfirmationCode(*r), r.StartTime.In(Location).Format("2006-01-02"))
	if err != nil || got.ActivationStatus != "active" || got.CanReschedule {
		t.Fatal(got, err)
	}
	replay, err := s.VerifySlotQR(ctx, ConfirmationCode(*r), testSlotQR(t, s), got.ReservationDate)
	if err != nil || !replay.QRVerifiedAt.Equal(*scanned.QRVerifiedAt) || replay.ActivationStatus != "active" {
		t.Fatal(replay, err)
	}
}
