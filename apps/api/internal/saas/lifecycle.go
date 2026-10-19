package saas

import (
	"time"
)

// State is one state of SubscriptionLifecycle: what a subscription in it allows and when it
// moves on. Durations come from the plan's published terms.
type State interface {
	Name() string
	// CanPost allows postings and every other write.
	CanPost() bool
	// CanExport allows reports, CSV exports and the full data export.
	CanExport() bool
	// next returns the subscription after the state's expiry on day today with the first day of
	// the new state, and false while the state still holds.
	next(s Subscription, p Plan, today time.Time) (Subscription, time.Time, bool)
}

// Transition is one state change of a subscription.
type Transition struct {
	From string
	To   string
	// On is the day the new state starts.
	On time.Time
}

// SubscriptionLifecycle owns the subscription states and their transition table:
//
//	trialing  --trial_ends_on passed-->            grace (grace_days)
//	active    --period end passed-->               grace (grace_days), or read_only when cancelled
//	grace     --grace_ends_on passed-->            read_only (read_only_days)
//	read_only --read_only_ends_on passed-->        closed
//
// Read-only keeps reads and exports and blocks every posting; nothing is ever deleted.
type SubscriptionLifecycle struct{}

// StateOf returns the state of a status; an unknown status is closed, which allows nothing.
func (SubscriptionLifecycle) StateOf(status string) State {
	switch status {
	case StatusTrialing:
		return trialing{}
	case StatusActive:
		return active{}
	case StatusGrace:
		return grace{}
	case StatusReadOnly:
		return readOnly{}
	default:
		return closed{}
	}
}

// Advance applies every transition due on day today, in order, and returns the resulting
// subscription with the transitions taken (none when the state still holds).
func (l SubscriptionLifecycle) Advance(s Subscription, p Plan, today time.Time) (Subscription, []Transition) {
	var out []Transition
	for range 5 { // a subscription passes through at most four states
		n, on, ok := l.StateOf(s.Status).next(s, p, today)
		if !ok {
			break
		}
		out = append(out, Transition{From: s.Status, To: n.Status, On: on})
		s = n
	}
	return s, out
}

func addDays(t time.Time, days int) *time.Time {
	d := t.AddDate(0, 0, days)
	return &d
}

type trialing struct{}

func (trialing) Name() string    { return StatusTrialing }
func (trialing) CanPost() bool   { return true }
func (trialing) CanExport() bool { return true }
func (trialing) next(s Subscription, p Plan, today time.Time) (Subscription, time.Time, bool) {
	ends := s.CurrentPeriodEnd
	if s.TrialEndsOn != nil {
		ends = *s.TrialEndsOn
	}
	if !today.After(ends) {
		return s, time.Time{}, false
	}
	s.Status, s.GraceEndsOn = StatusGrace, addDays(ends, p.Term(TermGraceDays))
	return s, ends.AddDate(0, 0, 1), true
}

type active struct{}

func (active) Name() string    { return StatusActive }
func (active) CanPost() bool   { return true }
func (active) CanExport() bool { return true }
func (active) next(s Subscription, p Plan, today time.Time) (Subscription, time.Time, bool) {
	if !today.After(s.CurrentPeriodEnd) {
		return s, time.Time{}, false
	}
	if s.CancelAtPeriodEnd {
		s.Status, s.ReadOnlyEndsOn = StatusReadOnly, addDays(s.CurrentPeriodEnd, p.Term(TermReadOnlyDays))
		return s, s.CurrentPeriodEnd.AddDate(0, 0, 1), true
	}
	s.Status, s.GraceEndsOn = StatusGrace, addDays(s.CurrentPeriodEnd, p.Term(TermGraceDays))
	return s, s.CurrentPeriodEnd.AddDate(0, 0, 1), true
}

type grace struct{}

func (grace) Name() string    { return StatusGrace }
func (grace) CanPost() bool   { return true }
func (grace) CanExport() bool { return true }
func (grace) next(s Subscription, p Plan, today time.Time) (Subscription, time.Time, bool) {
	if s.GraceEndsOn == nil || !today.After(*s.GraceEndsOn) {
		return s, time.Time{}, false
	}
	on := s.GraceEndsOn.AddDate(0, 0, 1)
	s.Status, s.ReadOnlyEndsOn = StatusReadOnly, addDays(*s.GraceEndsOn, p.Term(TermReadOnlyDays))
	return s, on, true
}

type readOnly struct{}

func (readOnly) Name() string    { return StatusReadOnly }
func (readOnly) CanPost() bool   { return false }
func (readOnly) CanExport() bool { return true }
func (readOnly) next(s Subscription, _ Plan, today time.Time) (Subscription, time.Time, bool) {
	if s.ReadOnlyEndsOn == nil || !today.After(*s.ReadOnlyEndsOn) {
		return s, time.Time{}, false
	}
	s.Status = StatusClosed
	return s, s.ReadOnlyEndsOn.AddDate(0, 0, 1), true
}

type closed struct{}

func (closed) Name() string    { return StatusClosed }
func (closed) CanPost() bool   { return false }
func (closed) CanExport() bool { return false }
func (closed) next(s Subscription, _ Plan, _ time.Time) (Subscription, time.Time, bool) {
	return s, time.Time{}, false
}

// tenantStatusFor is the tenants.status a subscription status implies.
func tenantStatusFor(status string) string {
	switch status {
	case StatusReadOnly:
		return "read_only"
	case StatusClosed:
		return "closed"
	default:
		return "active"
	}
}
