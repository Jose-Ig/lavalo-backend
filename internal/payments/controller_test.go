package payments

import (
	"context"
	"errors"
	"github.com/Jose-Ig/lavalo-backend/internal/controllers"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	guuid "github.com/google/uuid"
	"testing"
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
	first, err := cs.Poll(ctx, deviceToken, guuid.NewString(), boot)
	if err != nil || !first.On || first.EndMS != parent.EndTime.UnixMilli() {
		t.Fatal(first, err)
	}
	if err = cs.Ack(ctx, deviceToken, first.CommandID, true); err != nil {
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
	next, err := cs.Poll(ctx, deviceToken, guuid.NewString(), boot)
	if err != nil || next.EndMS != child.EndTime.UnixMilli() || next.Revision <= first.Revision || next.CommandID == first.CommandID {
		t.Fatal("paid extension not propagated", next, err)
	}
	if err = cs.Ack(ctx, deviceToken, first.CommandID, true); !errors.Is(err, controllers.ErrStale) {
		t.Fatal("old deadline ack accepted", err)
	}
	if err = cs.Ack(ctx, deviceToken, next.CommandID, true); err != nil {
		t.Fatal(err)
	}
}
