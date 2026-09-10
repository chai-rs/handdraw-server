package model

import "time"

// Idle tracks user activity independently of transport heartbeats and token refreshes.
type Idle struct {
	activity time.Time
	deadline time.Time
	seconds  int
}

// NewIdle starts a tier deadline; zero or unsupported durations fail closed.
func NewIdle(now time.Time, seconds int) (Idle, error) {
	if seconds != 7200 && seconds != 14400 {
		return Idle{}, ErrSession
	}

	return Idle{activity: now, deadline: now.Add(time.Duration(seconds) * time.Second), seconds: seconds}, nil
}

// Deadline is authoritative until another accepted user action or tier change.
func (i Idle) Deadline() time.Time { return i.deadline }

// Active refuses activity at or after the deadline, including late keep-alive requests.
func (i Idle) Active(now time.Time) bool { return now.Before(i.deadline) }

// Touch extends an active session from real user interaction.
func (i *Idle) Touch(now time.Time) bool {
	if !i.Active(now) {
		return false
	}

	i.activity = now
	i.deadline = now.Add(time.Duration(i.seconds) * time.Second)

	return true
}

// SetTier derives the new deadline from the last interaction and never revives an expired session.
func (i *Idle) SetTier(now time.Time, seconds int) bool {
	if !i.Active(now) || (seconds != 7200 && seconds != 14400) {
		return false
	}

	i.seconds = seconds
	i.deadline = i.activity.Add(time.Duration(seconds) * time.Second)

	return i.Active(now)
}
