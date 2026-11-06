package controllers

import (
	"context"
	"log"
	"time"

	"github.com/Jose-Ig/lavalo-backend/internal/operations"
	reservation "github.com/Jose-Ig/lavalo-backend/internal/reservations/domain/models"
	slot "github.com/Jose-Ig/lavalo-backend/internal/slots/domain/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxSessionMS int64 = 12 * 60 * 60 * 1000

func max[T ~int64 | ~uint64](a, b T) T {
	if a > b {
		return a
	}
	return b
}
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

type Checkpoint struct {
	SessionID   string `json:"session_id"`
	ExecutionID string `json:"execution_id"`
	Revision    uint64 `json:"revision"`
	Sequence    uint64 `json:"sequence"`
	RemainingMS int64  `json:"remaining_ms"`
	Completed   bool   `json:"completed"`
}

type BootstrapInput struct {
	RequestID string      `json:"request_id"`
	BootID    string      `json:"boot_id"`
	Protocol  int         `json:"protocol"`
	Ready     bool        `json:"ready"`
	Previous  *Checkpoint `json:"previous_session,omitempty"`
}

func (s *Service) current(tx *gorm.DB, d *Device) (*operations.Session, error) {
	if d.SessionID == "" {
		return nil, nil
	}
	var session operations.Session
	if err := tx.First(&session, "id = ? AND device_id = ?", d.SessionID, d.ID).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

func off(d *Device) {
	if d.CommandID == "" || d.EndMS != 0 {
		d.CommandID = uuid.NewString()
		d.Revision++
		d.EndMS = 0
		d.AckOn = false
		d.AckAt = nil
		d.AckCommandID = ""
	}
}

func finish(tx *gorm.DB, session *operations.Session, state, reason string) error {
	if operations.Terminal(session.State) {
		return nil
	}
	session.State, session.Reason = state, reason
	session.RemainingMS, session.LastRemainingMS, session.ExtraMS = 0, 0, 0
	session.Version++
	return tx.Save(session).Error
}

func resolve(tx *gorm.DB, session *operations.Session, resolution string, now time.Time) error {
	return tx.Model(&operations.Incident{}).Where("execution_id = ? AND resolved_at IS NULL", session.ExecutionID).
		Updates(map[string]any{"resolution": resolution, "resolved_at": now}).Error
}

func interrupt(tx *gorm.DB, session *operations.Session, now time.Time, force bool) error {
	if operations.Terminal(session.State) || session.State == operations.Pending || session.State == operations.Lost {
		return nil
	}
	if !force && session.LastReportAt != nil && now.Sub(*session.LastReportAt) <= OnlineWindow {
		return nil
	}
	if !force && session.LastReportAt == nil && now.UnixMilli()-session.StartMS <= OnlineWindow.Milliseconds() {
		return nil
	}
	session.RemainingMS = max(0, session.LastRemainingMS)
	incident := operations.Incident{SessionID: session.ID, ExecutionID: session.ExecutionID,
		LastReportAt: session.LastReportAt, DetectedAt: now, FrozenMS: session.RemainingMS}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&incident).Error; err != nil {
		return err
	}
	session.State, session.Reason = operations.Lost, "communication_lost"
	session.Version++
	return tx.Save(session).Error
}

// A report describes an applied command, not measured voltage or motor operation.
func applyReport(tx *gorm.DB, session *operations.Session, cp *Checkpoint, now time.Time, recovery bool) (bool, error) {
	if cp == nil || cp.SessionID != session.ID || cp.Sequence == 0 {
		return false, nil
	}
	// A previous record is valid only through unacknowledged descendants.
	// Firmware persists a command before enabling GPIO, so an older durable
	// execution proves a later, never-reported authorization was not applied.
	var descendants []operations.Execution
	cursor := session.ExecutionID
	for cursor != cp.ExecutionID {
		if !recovery || len(descendants) >= 64 {
			return false, nil
		}
		var next operations.Execution
		if err := tx.First(&next, "id = ? AND session_id = ?", cursor, session.ID).Error; err != nil {
			return false, err
		}
		if next.Sequence != 0 || next.SourceExecutionID == "" {
			return false, nil
		}
		descendants = append(descendants, next)
		cursor = next.SourceExecutionID
	}
	var ex operations.Execution
	if err := tx.First(&ex, "id = ? AND session_id = ?", cp.ExecutionID, session.ID).Error; err != nil {
		return false, err
	}
	if cp.Revision != ex.Revision || cp.RemainingMS < 0 || cp.RemainingMS > ex.BudgetMS || (cp.Completed && cp.RemainingMS != 0) {
		return false, nil
	}
	if !recovery && (cp.Sequence < ex.Sequence || cp.RemainingMS > ex.RemainingMS) {
		return false, nil
	}
	if recovery && cp.Sequence < ex.Sequence && cp.RemainingMS < ex.RemainingMS {
		return false, nil
	}
	remaining := min(cp.RemainingMS, ex.RemainingMS)
	ex.Sequence = max(ex.Sequence, cp.Sequence)
	ex.RemainingMS = remaining
	ex.Completed = ex.Completed || cp.Completed || remaining == 0
	if err := tx.Save(&ex).Error; err != nil {
		return false, err
	}
	for i := len(descendants) - 1; i >= 0; i-- {
		remaining = min(remaining, descendants[i].SourceRemainingMS) + descendants[i].AdditionalMS
	}
	session.LastRemainingMS, session.RemainingMS = remaining, remaining
	session.LastReportAt = &now
	session.Version++
	if remaining == 0 && session.ExtraMS == 0 {
		if err := resolve(tx, session, "completed", now); err != nil {
			return false, err
		}
		return true, finish(tx, session, operations.Completed, "time_used")
	}
	return true, tx.Save(session).Error
}

func eligible(tx *gorm.DB, session *operations.Session, sl slot.Slot) (bool, error) {
	var r reservation.Reservation
	if err := tx.First(&r, session.ReservationID).Error; err != nil {
		return false, err
	}
	if r.Status != "confirmed" || r.QRVerifiedAt == nil || r.SlotID != sl.ID {
		return false, finish(tx, session, operations.Closed, "reservation_closed")
	}
	return sl.IsAvailable, nil
}

func intervalFree(tx *gorm.DB, session operations.Session, now time.Time, duration int64) (bool, error) {
	end := now.Add(time.Duration(duration) * time.Millisecond)
	var n int64
	err := tx.Model(&reservation.Reservation{}).Where("slot_id = ? AND id <> ? AND (parent_reservation_id IS NULL OR parent_reservation_id <> ?) AND start_time < ? AND end_time > ?", session.SlotID, session.ReservationID, session.ReservationID, end, now).
		Where("status = ? OR (status = ? AND (expires_at IS NULL OR expires_at > ?))", "confirmed", "pending", now).Count(&n).Error
	if err != nil || n > 0 {
		return false, err
	}
	busy, err := operations.Occupied(tx, session.SlotID, session.ReservationID, now, end, now)
	return !busy, err
}

func authorize(tx *gorm.DB, d *Device, session *operations.Session, now time.Time, remaining int64) error {
	if remaining <= 0 {
		return finish(tx, session, operations.Completed, "time_used")
	}
	if remaining > MaxSessionMS {
		session.State, session.Reason = operations.Pending, "duration_out_of_range"
		off(d)
		return tx.Save(session).Error
	}
	if err := resolve(tx, session, "resumed", now); err != nil {
		return err
	}
	d.CommandID = uuid.NewString()
	d.Revision++
	d.ReservationID = session.ReservationID
	d.EndMS = now.UnixMilli() + remaining
	d.AckOn, d.AckAt, d.AckCommandID = false, nil, ""
	ex := operations.Execution{ID: uuid.NewString(), SessionID: session.ID, BootID: d.BootID,
		CommandID: d.CommandID, Revision: d.Revision, StartMS: now.UnixMilli(), EndMS: d.EndMS, BudgetMS: remaining, RemainingMS: remaining,
		SourceExecutionID: session.ExecutionID, SourceRemainingMS: remaining - session.ExtraMS, AdditionalMS: session.ExtraMS}
	if err := tx.Create(&ex).Error; err != nil {
		return err
	}
	session.ExecutionID, session.State, session.Reason = ex.ID, operations.Recovering, "awaiting_controller"
	session.StartMS, session.EndMS = ex.StartMS, ex.EndMS
	session.RemainingMS, session.LastRemainingMS = remaining, remaining
	session.ExtraMS, session.LastReportAt = 0, nil
	session.Version++
	d.SessionID = session.ID
	return tx.Save(session).Error
}

func makeCommand(d Device, session *operations.Session, nonce string, now time.Time) Command {
	c := Command{RequestID: nonce, BootID: d.BootID, Protocol: 2, CommandID: d.CommandID, Revision: d.Revision,
		ServerMS: now.UnixMilli(), PollMS: 2500, State: "idle"}
	if session == nil {
		return c
	}
	c.SessionID, c.ExecutionID = session.ID, session.ExecutionID
	c.State, c.Reason = session.State, session.Reason
	c.RemainingMS = operations.Remaining(*session, now)
	c.On = !operations.Terminal(session.State) && session.State != operations.Pending && d.EndMS > now.UnixMilli()
	if c.On {
		c.EndMS = d.EndMS
	}
	return c
}

// Bootstrap is idempotent per device/boot. A duplicate cannot extend a deadline.
func (s *Service) Bootstrap(ctx context.Context, token string, input BootstrapInput) (Command, error) {
	var c Command
	if input.Protocol != 2 {
		return c, ErrBootstrap
	}
	err := s.withDevice(ctx, token, func(tx *gorm.DB, d *Device, sl slot.Slot) error {
		now := s.Now()
		session, err := s.current(tx, d)
		if err != nil {
			return err
		}
		changed := d.BootID != input.BootID
		if changed {
			var seen int64
			if err := tx.Model(&operations.Boot{}).Where("device_id = ? AND boot_id = ?", d.ID, input.BootID).Count(&seen).Error; err != nil {
				return err
			}
			if seen > 0 {
				return ErrStale
			}
			if session != nil && !operations.Terminal(session.State) {
				if err := interrupt(tx, session, now, true); err != nil {
					return err
				}
				valid, err := applyReport(tx, session, input.Previous, now, true)
				if err != nil {
					return err
				}
				if !operations.Terminal(session.State) {
					session.State, session.Reason = operations.Pending, "checkpoint_missing_or_invalid"
					if valid {
						session.Reason = "resume_ready"
					}
					session.Version++
					if err := tx.Save(session).Error; err != nil {
						return err
					}
				}
			}
			d.BootID, d.Protocol = input.BootID, 2
			off(d)
			// Invalidate even an OFF command from an older process.
			d.CommandID, d.AckCommandID, d.AckAt = uuid.NewString(), "", nil
			d.Revision++
			if err := tx.Create(&operations.Boot{DeviceID: d.ID, BootID: d.BootID}).Error; err != nil {
				return err
			}
		}
		if session != nil && !operations.Terminal(session.State) {
			ok, err := eligible(tx, session, sl)
			if err != nil {
				return err
			}
			if ok && input.Ready && session.State == operations.Pending && session.Reason == "resume_ready" {
				remaining := session.RemainingMS + session.ExtraMS
				free, err := intervalFree(tx, *session, now, remaining)
				if err != nil {
					return err
				}
				if free {
					if err := authorize(tx, d, session, now, remaining); err != nil {
						return err
					}
				} else {
					session.Reason = "slot_conflict"
					if err := tx.Save(session).Error; err != nil {
						return err
					}
				}
			}
			if !ok && !operations.Terminal(session.State) {
				session.State, session.Reason = operations.Pending, "slot_unavailable"
				session.Version++
				if err := tx.Save(session).Error; err != nil {
					return err
				}
			}
			if !ok || session.State == operations.Pending || operations.Terminal(session.State) {
				off(d)
			}
		}
		d.LastSeenAt = &now
		if err := tx.Save(d).Error; err != nil {
			return err
		}
		c = makeCommand(*d, session, input.RequestID, now)
		return nil
	})
	return c, err
}

func (s *Service) Poll(ctx context.Context, token, nonce, bootID string) (Command, error) {
	return s.PollReport(ctx, token, nonce, bootID, nil)
}

func (s *Service) PollReport(ctx context.Context, token, nonce, bootID string, cp *Checkpoint) (Command, error) {
	var c Command
	err := s.withDevice(ctx, token, func(tx *gorm.DB, d *Device, sl slot.Slot) error {
		if d.Protocol != 2 || d.BootID != bootID {
			return ErrBootstrap
		}
		now := s.Now()
		session, err := s.current(tx, d)
		if err != nil {
			return err
		}
		if session != nil && !operations.Terminal(session.State) {
			if err := interrupt(tx, session, now, false); err != nil {
				return err
			}
			// If an authorization expired without ever being applied, the
			// durable older record can reconcile its unacknowledged lineage.
			if cp != nil && cp.ExecutionID != session.ExecutionID && now.UnixMilli() >= session.EndMS && session.State != operations.Pending {
				recovered, err := applyReport(tx, session, cp, now, true)
				if err != nil {
					return err
				}
				if recovered && !operations.Terminal(session.State) {
					free, err := intervalFree(tx, *session, now, session.RemainingMS+session.ExtraMS)
					if err != nil {
						return err
					}
					if free && sl.IsAvailable {
						if err := authorize(tx, d, session, now, session.RemainingMS+session.ExtraMS); err != nil {
							return err
						}
					} else {
						session.State, session.Reason = operations.Pending, "slot_conflict"
						off(d)
						if err := tx.Save(session).Error; err != nil {
							return err
						}
					}
				}
			}
			valid, err := applyReport(tx, session, cp, now, false)
			if err != nil {
				return err
			}
			ok, err := eligible(tx, session, sl)
			if err != nil {
				return err
			}
			if ok && session.State != operations.Pending && !operations.Terminal(session.State) {
				if now.UnixMilli() >= session.EndMS && session.ExtraMS == 0 && !valid {
					session.State, session.Reason = operations.Pending, "checkpoint_missing_or_invalid"
					session.RemainingMS = session.LastRemainingMS
					if err := tx.Save(session).Error; err != nil {
						return err
					}
				} else if now.UnixMilli() >= session.EndMS && session.ExtraMS == 0 {
					if err := resolve(tx, session, "completed", now); err != nil {
						return err
					}
					if err := finish(tx, session, operations.Completed, "time_used"); err != nil {
						return err
					}
				} else if valid {
					if session.State == operations.Lost {
						if err := resolve(tx, session, "same_boot_reconnected", now); err != nil {
							return err
						}
					}
					session.State, session.Reason = operations.Running, ""
					if session.ExtraMS > 0 {
						remaining := max(0, session.EndMS-now.UnixMilli()) + session.ExtraMS
						free, err := intervalFree(tx, *session, now, remaining)
						if err != nil {
							return err
						}
						if free {
							if err := authorize(tx, d, session, now, remaining); err != nil {
								return err
							}
						} else {
							session.State, session.Reason = operations.Pending, "slot_conflict"
							session.RemainingMS = session.LastRemainingMS
							off(d)
							if err := tx.Save(session).Error; err != nil {
								return err
							}
						}
					} else if err := tx.Save(session).Error; err != nil {
						return err
					}
				}
			}
			if !ok && !operations.Terminal(session.State) {
				session.State, session.Reason = operations.Pending, "slot_unavailable"
				session.Version++
				if err := tx.Save(session).Error; err != nil {
					return err
				}
			}
			if !ok || session.State == operations.Pending || operations.Terminal(session.State) {
				off(d)
			}
		}
		// Never replace an unresolved older session with a newer reservation.
		if session == nil || operations.Terminal(session.State) {
			off(d)
			var rs []reservation.Reservation
			if sl.IsAvailable {
				err = tx.Where("slot_id = ? AND parent_reservation_id IS NULL AND status = ? AND qr_verified_at IS NOT NULL AND start_time <= ? AND end_time > ?", sl.ID, "confirmed", now, now).Limit(2).Find(&rs).Error
				if err != nil {
					return err
				}
			}
			if len(rs) == 1 {
				r := rs[0]
				old, found, err := operations.Find(tx, r.ID)
				if err != nil {
					return err
				}
				if !found {
					fresh := operations.Session{ID: uuid.NewString(), ReservationID: r.ID, SlotID: sl.ID, DeviceID: d.ID,
						State: operations.Recovering, PaidMinutes: r.DurationMinutes, Version: 1}
					free, err := intervalFree(tx, fresh, now, r.EndTime.Sub(now).Milliseconds())
					if err != nil {
						return err
					}
					if free {
						if err := tx.Create(&fresh).Error; err != nil {
							return err
						}
						session = &fresh
						if err := authorize(tx, d, session, now, r.EndTime.Sub(now).Milliseconds()); err != nil {
							return err
						}
					}
				} else if old.DeviceID == d.ID {
					session = &old
				}
			}
		}
		d.LastSeenAt = &now
		if err := tx.Save(d).Error; err != nil {
			return err
		}
		c = makeCommand(*d, session, nonce, now)
		return nil
	})
	return c, err
}

func (s *Service) AckForBoot(ctx context.Context, token, bootID, commandID string, on bool, cp *Checkpoint) error {
	return s.withDevice(ctx, token, func(tx *gorm.DB, d *Device, sl slot.Slot) error {
		if d.Protocol != 2 || d.BootID != bootID {
			return ErrBootstrap
		}
		now := s.Now()
		if d.CommandID != commandID || on != (d.EndMS > now.UnixMilli()) {
			return ErrStale
		}
		session, err := s.current(tx, d)
		if err != nil {
			return err
		}
		if on {
			if session == nil || operations.Terminal(session.State) || session.State == operations.Pending || !sl.IsAvailable {
				return ErrStale
			}
			ok, err := eligible(tx, session, sl)
			if err != nil {
				return err
			}
			if !ok {
				return ErrStale
			}
			valid, err := applyReport(tx, session, cp, now, false)
			if err != nil {
				return err
			}
			if !valid || operations.Terminal(session.State) {
				return ErrStale
			}
			session.State, session.Reason = operations.Running, ""
			if err := tx.Save(session).Error; err != nil {
				return err
			}
		}
		d.AckCommandID, d.AckOn, d.AckAt, d.LastSeenAt = commandID, on, &now, &now
		return tx.Save(d).Error
	})
}

// RunMonitor persists interruptions even when every customer app is closed.
func (s *Service) RunMonitor(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Monitor(ctx); err != nil {
				log.Print("Controller interruption monitor failed; retrying on next tick")
			}
		}
	}
}

func (s *Service) Monitor(ctx context.Context) error {
	var devices []Device
	if err := s.db(ctx).Where("session_id <> ?", "").Find(&devices).Error; err != nil {
		return err
	}
	for _, known := range devices {
		err := s.db(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&slot.Slot{}).Where("id = ?", known.SlotID).Update("updated_at", s.Now()).Error; err != nil {
				return err
			}
			if err := tx.Model(&Device{}).Where("id = ?", known.ID).Update("updated_at", s.Now()).Error; err != nil {
				return err
			}
			var d Device
			if err := tx.First(&d, "id = ?", known.ID).Error; err != nil {
				return err
			}
			session, err := s.current(tx, &d)
			if err != nil || session == nil {
				return err
			}
			return interrupt(tx, session, s.Now(), false)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

type View struct {
	Status      string `json:"-"`
	State       string `json:"operational_state"`
	Reason      string `json:"operational_reason,omitempty"`
	RemainingMS int64  `json:"remaining_ms"`
	EndMS       int64  `json:"operational_end_ms"`
	ServerMS    int64  `json:"server_ms"`
	Version     uint64 `json:"operational_version"`
	Frozen      bool   `json:"timer_frozen"`
}

func Snapshot(db *gorm.DB, r reservation.Reservation, now time.Time) (View, error) {
	v := View{Status: "not_started", State: "not_started", ServerMS: now.UnixMilli(), EndMS: r.EndTime.UnixMilli(), RemainingMS: max(0, r.EndTime.Sub(now).Milliseconds())}
	if r.QRVerifiedAt == nil {
		return v, nil
	}
	var d Device
	result := db.Where("slot_id = ?", r.SlotID).Limit(1).Find(&d)
	err := result.Error
	if result.RowsAffected == 0 && err == nil {
		v.Status, v.State = "simulated", "simulated"
		if r.Status != "confirmed" || !now.Before(r.EndTime) {
			v.Status, v.State, v.RemainingMS = "ended", operations.Completed, 0
		}
		return v, nil
	}
	if err != nil {
		return v, err
	}
	session, found, err := operations.Find(db, r.ID)
	if err != nil {
		return v, err
	}
	if found {
		v.State, v.Reason, v.Version = session.State, session.Reason, session.Version
		v.EndMS, v.RemainingMS = session.EndMS, operations.Remaining(session, now)
		v.Frozen = operations.Frozen(session, now)
		if v.Frozen {
			v.RemainingMS = max(0, session.LastRemainingMS+session.ExtraMS)
			v.State = operations.Lost
		}
		if session.State == operations.Pending {
			v.State, v.RemainingMS, v.Frozen = operations.Pending, max(0, session.RemainingMS+session.ExtraMS), true
		}
		if operations.Terminal(session.State) {
			v.Status, v.RemainingMS = "ended", 0
			return v, nil
		}
	}
	if !found && !now.Before(r.EndTime) {
		v.Status, v.State, v.RemainingMS = "ended", operations.Completed, 0
		return v, nil
	}
	if r.Status != "confirmed" {
		v.Status, v.State, v.RemainingMS = "ended", operations.Closed, 0
		return v, nil
	}
	if !d.Enabled {
		v.Status, v.Frozen = "controller_unavailable", true
		return v, nil
	}
	if d.LastSeenAt == nil || now.Sub(*d.LastSeenAt) > OnlineWindow || v.State == operations.Lost {
		v.Status, v.Frozen = "controller_offline", true
		return v, nil
	}
	if found && session.State == operations.Pending {
		v.Status = "pending_recovery"
		return v, nil
	}
	if found && session.State == operations.Running && d.SessionID == session.ID && d.AckOn && d.AckCommandID == d.CommandID && d.EndMS == session.EndMS && session.ExtraMS == 0 {
		v.Status = "active"
	} else {
		v.Status = "awaiting_controller"
	}
	return v, nil
}

func Activation(db *gorm.DB, r reservation.Reservation, now time.Time) (string, error) {
	v, err := Snapshot(db, r, now)
	return v.Status, err
}
