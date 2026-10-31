package payments

import (
	"context"
	"errors"
	"github.com/Jose-Ig/lavalo-backend/internal/bookings"
	"github.com/gin-gonic/gin"
	"testing"
	"time"
)

func TestSimulationConfirmsIndependentCodeAndCannotCancel(t *testing.T) {
	s, _, id, token := fixture(t)
	s.Config.MockPayments = true
	s.Config.Enabled = false // Simulation does not need provider credentials.
	s.Gateway = nil          // Any provider call would panic.
	ctx := context.Background()
	if _, err := s.Start(ctx, id, "wrong"); err == nil {
		t.Fatal("invalid token accepted")
	}
	v, err := s.Start(ctx, id, token)
	if err != nil || !v.Simulated || !v.Verified || v.State != "completed" || v.ReservationStatus != "confirmed" || v.ConfirmationCode == "" {
		t.Fatal(v, err)
	}
	again, err := s.Start(ctx, id, token)
	if err != nil || again.ConfirmationCode != v.ConfirmationCode {
		t.Fatal("retry changed code", err)
	}
	status, err := s.Status(ctx, id, token)
	if err != nil || !status.Simulated || status.ConfirmationCode != v.ConfirmationCode {
		t.Fatal(status, err)
	}
	access, err := s.Bookings.LookupCode(ctx, v.ConfirmationCode)
	if err != nil || access.ID != id {
		t.Fatal(access, err)
	}
	if err := s.Bookings.Cancel(ctx, id, token); !errors.Is(err, bookings.ErrConflict) {
		t.Fatal("paid booking cancelled", err)
	}
	var count int64
	s.DB.Model(&Checkout{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate checkout", count)
	}
	s.DB.Model(&bookings.NotificationIntent{}).Count(&count)
	if count != 1 {
		t.Fatal("expected one SMS intent", count)
	}
	s.Config.MockPayments = false
	if _, err := s.Status(ctx, id, token); !errors.Is(err, ErrUnavailable) {
		t.Fatal("simulation trusted with mode disabled", err)
	}
}
func TestSimulationRejectsExpiredCancelledAndRealAttempts(t *testing.T) {
	for _, kind := range []string{"expired", "cancelled", "real"} {
		t.Run(kind, func(t *testing.T) {
			s, _, id, token := fixture(t)
			ctx := context.Background()
			switch kind {
			case "expired":
				now := s.Now().Add(11 * time.Minute)
				s.Now = func() time.Time { return now }
			case "cancelled":
				if err := s.Bookings.Cancel(ctx, id, token); err != nil {
					t.Fatal(err)
				}
			case "real":
				if _, err := s.Start(ctx, id, token); err != nil {
					t.Fatal(err)
				}
			}
			s.Config.MockPayments = true
			if _, err := s.Start(ctx, id, token); !errors.Is(err, ErrConflict) {
				t.Fatal("ineligible simulation accepted", err)
			}
		})
	}
}
func TestSimulationRequiresExplicitDebugAndNeverLiveOrRelease(t *testing.T) {
	t.Setenv("MP_MOCK_PAYMENTS", "true")
	for _, mode := range []string{"", "release", "debug"} {
		t.Setenv("GIN_MODE", mode)
		if ConfigFromEnv().MockPayments != (mode == "debug") {
			t.Fatal("bad environment gate", mode)
		}
	}
	s, _, id, token := fixture(t)
	s.Config.MockPayments = true
	s.Config.LiveMode = true
	if _, err := s.simulatePayment(context.Background(), id, token); !errors.Is(err, ErrUnavailable) {
		t.Fatal("live simulation accepted", err)
	}
	s.Config.LiveMode = false
	previous := gin.Mode()
	gin.SetMode(gin.ReleaseMode)
	defer gin.SetMode(previous)
	if _, err := s.simulatePayment(context.Background(), id, token); !errors.Is(err, ErrUnavailable) {
		t.Fatal("release simulation accepted", err)
	}
}
