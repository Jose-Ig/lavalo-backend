package bookings

import (
	"context"
	"errors"
	"fmt"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRescheduleKeepsPaidBookingAndCodeAndReleasesOldTime(t *testing.T) {
	s, r, token := confirmedFixture(t)
	ctx := context.Background()
	code := ConfirmationCode(*r)
	oldDay := r.StartTime.In(Location).Format("2006-01-02")
	slots, err := s.RescheduleAvailability(ctx, code, oldDay, r.StartTime)
	if err != nil || len(slots) != 1 {
		t.Fatal("own slot excluded", slots, err)
	}
	next := r.StartTime.AddDate(0, 0, 1)
	result, err := s.Reschedule(ctx, code, oldDay, r.SlotID, next)
	if err != nil || result.ID != r.ID || result.Code != code || result.Status != "confirmed" || result.Duration != r.DurationMinutes || result.ReservationNumber != next.In(Location).Format("02012006-1504") {
		t.Fatal(result, err)
	}
	stored, err := s.Get(ctx, r.ID, token)
	if err != nil || stored.AccessTokenHash != r.AccessTokenHash || stored.Phone != r.Phone {
		t.Fatal("contact/token changed", err)
	}
	if _, err = s.LookupCode(ctx, code, oldDay); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("old date accepted", err)
	}
	if _, err = s.LookupCode(ctx, code, result.ReservationDate); err != nil {
		t.Fatal(err)
	}
	free, err := s.Available(ctx, r.StartTime, r.DurationMinutes)
	if err != nil || len(free) != 1 {
		t.Fatal("old turn not freed", err)
	}
	free, err = s.Available(ctx, next, r.DurationMinutes)
	if err != nil || len(free) != 0 {
		t.Fatal("new turn not occupied", err)
	}
}
func TestRescheduleRejectsConflictAndLeavesOriginalUntouched(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	code := ConfirmationCode(*r)
	day := r.StartTime.In(Location).Format("2006-01-02")
	target := r.EndTime
	if _, _, err := s.Create(context.Background(), Input{SlotID: r.SlotID, StartTime: target, Duration: r.DurationMinutes, Name: r.Name, Phone: r.Phone}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reschedule(context.Background(), code, day, r.SlotID, target); !errors.Is(err, ErrConflict) {
		t.Fatal("overlap accepted", err)
	}
	got, err := s.LookupCode(context.Background(), code, day)
	if err != nil || !got.StartTime.Equal(r.StartTime) {
		t.Fatal("original changed", got, err)
	}
	for _, target := range []time.Time{s.Now().Add(-time.Hour), r.StartTime.Add(time.Minute), r.StartTime.AddDate(0, 0, 9)} {
		if _, err := s.Reschedule(context.Background(), code, day, r.SlotID, target); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid time accepted", err)
		}
	}
	if _, err := s.Reschedule(context.Background(), "wrong", day, r.SlotID, r.EndTime); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("bad code accepted", err)
	}
}
func TestRescheduleBlocksStartedValidatedAndUnpaidBookings(t *testing.T) {
	for _, state := range []string{"pending", "cancelled", "completed", "started", "qr"} {
		t.Run(state, func(t *testing.T) {
			s, r, _ := confirmedFixture(t)
			code := ConfirmationCode(*r)
			day := r.StartTime.In(Location).Format("2006-01-02")
			switch state {
			case "started":
				now := r.StartTime
				s.Now = func() time.Time { return now }
			case "qr":
				now := s.Now()
				s.DB.Model(r).Update("qr_verified_at", now)
			default:
				s.DB.Model(r).Update("status", state)
			}
			if _, err := s.Reschedule(context.Background(), code, day, r.SlotID, r.EndTime); !errors.Is(err, ErrConflict) {
				t.Fatal("ineligible booking moved", err)
			}
			if _, err := s.RescheduleAvailability(context.Background(), code, day, r.EndTime); !errors.Is(err, ErrConflict) {
				t.Fatal("ineligible availability allowed", err)
			}
		})
	}
}
func TestRescheduleCanChangeSpaceButNotPaidDuration(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	s.DB.Create(&slot.Slot{ID: 2, Label: "Two", IsAvailable: true})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterCodeRoutes(router.Group("/api/v1"), s)
	body := fmt.Sprintf(`{"code":%q,"date":%q,"slot_id":2,"start_time":%q,"duration_minutes":15}`, ConfirmationCode(*r), r.StartTime.In(Location).Format("2006-01-02"), r.StartTime.Format(time.RFC3339))
	req := httptest.NewRequest("POST", "/api/v1/reservations/reschedule", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got reservation.Reservation
	s.DB.First(&got, r.ID)
	if got.SlotID != 2 || got.DurationMinutes != r.DurationMinutes || got.EndTime.Sub(got.StartTime) != time.Duration(r.DurationMinutes)*time.Minute {
		t.Fatal("paid duration changed", got)
	}
}
func TestRescheduleResolvesDailyCodeCollision(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	code := ConfirmationCode(*r)
	oldDay := r.StartTime.In(Location).Format("2006-01-02")
	next := r.StartTime.AddDate(0, 0, 1)
	hash := dailyCodeHash(next.In(Location).Format("2006-01-02"), code)
	other := reservation.Reservation{SlotID: r.SlotID, StartTime: next.Add(time.Hour), CodeHash: &hash, CodeVersion: 2, Status: "cancelled"}
	if err := s.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	result, err := s.Reschedule(context.Background(), code, oldDay, r.SlotID, next)
	if err != nil || result.Code == code || len(result.Code) != 6 {
		t.Fatal("collision not resolved", result, err)
	}
	if _, err = s.LookupCode(context.Background(), result.Code, result.ReservationDate); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentReschedulesCannotOccupySameSpace(t *testing.T) {
	s, r, _ := confirmedFixture(t)
	second, _, err := s.Create(context.Background(), Input{SlotID: r.SlotID, StartTime: r.EndTime, Duration: r.DurationMinutes, Name: r.Name, Phone: r.Phone})
	if err != nil {
		t.Fatal(err)
	}
	second.Status = "confirmed"
	s.DB.Model(second).Update("status", "confirmed")
	if err = s.DB.Transaction(func(tx *gorm.DB) error { return EnsureConfirmation(tx, second) }); err != nil {
		t.Fatal(err)
	}
	target := second.EndTime.Add(time.Hour)
	results := make(chan error, 2)
	for _, r := range []*reservation.Reservation{r, second} {
		go func(r *reservation.Reservation) {
			_, err := s.Reschedule(context.Background(), ConfirmationCode(*r), r.StartTime.In(Location).Format("2006-01-02"), r.SlotID, target)
			results <- err
		}(r)
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
		t.Fatal("double booking", wins, conflicts)
	}
}
