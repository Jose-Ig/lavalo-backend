package payments

import (
	"context"
	"errors"
	"github.com/Jose-Ig/lavalo-backend/internal/controllers"
	"github.com/Jose-Ig/lavalo-backend/internal/operations"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	guuid "github.com/google/uuid"
	"testing"
	"time"
)

func TestOnlyVerifiedExtensionPaymentChangesControllerDeadline(t *testing.T) {
	s, _, parent, child, token := extensionFixture(t)
	ctx := context.Background()
	cs := controllers.New(s.DB)
	cs.Now = s.Now
	var sl slot.Slot
	s.DB.First(&sl, parent.SlotID)
	_, deviceToken, err := cs.Provision(ctx, sl.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	boot := guuid.NewString()
	if _, err = cs.Bootstrap(ctx, deviceToken, controllers.BootstrapInput{RequestID: guuid.NewString(), BootID: boot, Protocol: 2, Ready: true}); err != nil {
		t.Fatal(err)
	}
	first, err := cs.Poll(ctx, deviceToken, guuid.NewString(), boot)
	if err != nil || !first.On || first.EndMS != parent.EndTime.UnixMilli() {
		t.Fatal(first, err)
	}
	if err = cs.AckForBoot(ctx, deviceToken, boot, first.CommandID, true, &controllers.Checkpoint{SessionID: first.SessionID, ExecutionID: first.ExecutionID, Revision: first.Revision, Sequence: 1, RemainingMS: first.EndMS - s.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(ctx, child.ID, token); err != nil {
		t.Fatal(err)
	}
	pending, err := cs.Poll(ctx, deviceToken, guuid.NewString(), boot)
	if err != nil || pending.CommandID != first.CommandID || pending.EndMS != first.EndMS {
		t.Fatal("unpaid extension enabled", pending, err)
	}
	var c Checkout
	s.DB.Where("reservation_id = ?", child.ID).First(&c)
	order := approved(s, c)
	order.Amount = "1250.00"
	order.PaidAmount = order.Amount
	if err = s.Apply(ctx, order); err != nil {
		t.Fatal(err)
	}
	next, err := cs.PollReport(ctx, deviceToken, guuid.NewString(), boot, &controllers.Checkpoint{SessionID: first.SessionID, ExecutionID: first.ExecutionID, Revision: first.Revision, Sequence: 2, RemainingMS: first.EndMS - s.Now().UnixMilli()})
	if err != nil || next.EndMS != child.EndTime.UnixMilli() || next.Revision <= first.Revision || next.CommandID == first.CommandID {
		t.Fatal("paid extension not propagated", next, err)
	}
	if err = cs.AckForBoot(ctx, deviceToken, boot, first.CommandID, true, &controllers.Checkpoint{SessionID: first.SessionID, ExecutionID: first.ExecutionID, Revision: first.Revision, Sequence: 1, RemainingMS: first.EndMS - s.Now().UnixMilli()}); !errors.Is(err, controllers.ErrStale) {
		t.Fatal("old deadline ack accepted", err)
	}
	if err = cs.AckForBoot(ctx, deviceToken, boot, next.CommandID, true, &controllers.Checkpoint{SessionID: next.SessionID, ExecutionID: next.ExecutionID, Revision: next.Revision, Sequence: 1, RemainingMS: next.EndMS - s.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionConfirmedDuringDisconnectAddsTimeOnce(t *testing.T) {
	s, _, parent, child, secret := extensionFixture(t)
	ctx := context.Background()
	now := s.Now()
	s.Now = func() time.Time { return now }
	s.Bookings.Now = s.Now
	cs := controllers.New(s.DB)
	cs.Now = s.Now
	var sl slot.Slot
	s.DB.First(&sl, parent.SlotID)
	_, token, err := cs.Provision(ctx, sl.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	id := guuid.NewString()
	if _, err = cs.Bootstrap(ctx, token, controllers.BootstrapInput{RequestID: guuid.NewString(), BootID: id, Protocol: 2, Ready: true}); err != nil {
		t.Fatal(err)
	}
	first, err := cs.Poll(ctx, token, guuid.NewString(), id)
	if err != nil || !first.On {
		t.Fatal(first, err)
	}
	cp := &controllers.Checkpoint{SessionID: first.SessionID, ExecutionID: first.ExecutionID, Revision: first.Revision, Sequence: 1, RemainingMS: first.RemainingMS}
	if err = cs.AckForBoot(ctx, token, id, first.CommandID, true, cp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(ctx, child.ID, secret); err != nil {
		t.Fatal(err)
	}
	now = now.Add(13 * time.Second)
	if err = cs.Monitor(ctx); err != nil {
		t.Fatal(err)
	}
	var checkout Checkout
	s.DB.Where("reservation_id = ?", child.ID).First(&checkout)
	order := approved(s, checkout)
	order.Amount = "1250.00"
	order.PaidAmount = order.Amount
	if err = s.Apply(ctx, order); err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(ctx, order); err != nil {
		t.Fatal(err)
	}
	session, found, err := operations.Find(s.DB, parent.ID)
	if err != nil || !found || session.ExtraMS != 15*60000 {
		t.Fatal(session, err)
	}
	cp.RemainingMS = first.EndMS - now.UnixMilli()
	cp.Sequence = 2
	next, err := cs.PollReport(ctx, token, guuid.NewString(), id, cp)
	if err != nil || !next.On || next.EndMS != first.EndMS+15*60000 {
		t.Fatal(next, err)
	}
}
