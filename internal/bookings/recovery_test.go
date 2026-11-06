package bookings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Jose-Ig/lavalo-backend/internal/controllers"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/google/uuid"
)

func TestRecoveredExecutionDrivesAppAvailabilityAndExtensions(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	ctx := context.Background()
	now := r.StartTime
	s.Now = func() time.Time { return now }
	cs := controllers.New(s.DB)
	cs.Now = s.Now
	var sl slot.Slot
	s.DB.First(&sl, r.SlotID)
	_, token, err := cs.Provision(ctx, sl.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err = cs.Bootstrap(ctx, token, controllers.BootstrapInput{RequestID: uuid.NewString(), BootID: id, Protocol: 2, Ready: true}); err != nil {
		t.Fatal(err)
	}
	day := r.StartTime.In(Location).Format("2006-01-02")
	if _, err = s.VerifySlotQR(ctx, ConfirmationCode(*r), "uash://slot/"+sl.PublicID, day); err != nil {
		t.Fatal(err)
	}
	initial, err := cs.Poll(ctx, token, uuid.NewString(), id)
	if err != nil || !initial.On {
		t.Fatal(initial, err)
	}
	cp := &controllers.Checkpoint{SessionID: initial.SessionID, ExecutionID: initial.ExecutionID, Revision: initial.Revision, Sequence: 1, RemainingMS: initial.RemainingMS}
	if err = cs.AckForBoot(ctx, token, id, initial.CommandID, true, cp); err != nil {
		t.Fatal(err)
	}
	now = r.EndTime.Add(time.Hour)
	cp.RemainingMS = 10 * 60000
	cp.Sequence = 30
	id = uuid.NewString()
	recovered, err := cs.Bootstrap(ctx, token, controllers.BootstrapInput{RequestID: uuid.NewString(), BootID: id, Protocol: 2, Ready: true, Previous: cp})
	if err != nil || !recovered.On {
		t.Fatal(recovered, err)
	}
	cp = &controllers.Checkpoint{SessionID: recovered.SessionID, ExecutionID: recovered.ExecutionID, Revision: recovered.Revision, Sequence: 1, RemainingMS: recovered.RemainingMS}
	if err = cs.AckForBoot(ctx, token, id, recovered.CommandID, true, cp); err != nil {
		t.Fatal(err)
	}
	view, err := s.LookupCode(ctx, ConfirmationCode(*r), day)
	if err != nil || view.ActivationStatus != "active" || view.RemainingMS != 10*60000 || view.EndMS != recovered.EndMS || !view.EndTime.Equal(r.EndTime) {
		t.Fatal(view, err)
	}
	if _, err = s.VerifySlotQR(ctx, ConfirmationCode(*r), "uash://slot/"+sl.PublicID, day); err != nil {
		t.Fatal("repeated QR changed recovery", err)
	}
	busy, err := s.busy(s.DB, r.SlotID, now.Add(time.Minute), now.Add(4*time.Minute))
	if err != nil || !busy {
		t.Fatal("recovered interval offered to another client", busy, err)
	}
	child, _, err := s.CreateExtension(ctx, ConfirmationCode(*r), day, 3)
	if err != nil || child.StartTime.UnixMilli() != recovered.EndMS || child.ParentExecutionID != recovered.ExecutionID {
		t.Fatal(child, err)
	}
	now = now.Add(13 * time.Second)
	if err = cs.Monitor(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExtensionOptions(ctx, ConfirmationCode(*r), day); !errors.Is(err, ErrConflict) {
		t.Fatal("extension offered during outage", err)
	}
	view, err = s.LookupCode(ctx, ConfirmationCode(*r), day)
	if err != nil || !view.Frozen || view.RemainingMS != 10*60000 {
		t.Fatal(view, err)
	}
}
