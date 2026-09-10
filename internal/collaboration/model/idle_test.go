package model_test

import (
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/internal/collaboration/model"
	"github.com/stretchr/testify/require"
)

// TestIdleRequiresActualActivityAndNeverRevivesExpiredSessions pins the tier timeout policy independently of wall-clock sleeps.
func TestIdleRequiresActualActivityAndNeverRevivesExpiredSessions(t *testing.T) {
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		seconds int
		elapsed time.Duration
		active  bool
	}{
		{"cloud before deadline", 7200, time.Hour, true},
		{"cloud at deadline", 7200, 2 * time.Hour, false},
		{"team active after two hours", 14400, 3 * time.Hour, true},
		{"team after deadline", 14400, 5 * time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idle, err := model.NewIdle(now, tc.seconds)
			require.NoError(t, err)
			require.Equal(t, tc.active, idle.Active(now.Add(tc.elapsed)))
			require.Equal(t, tc.active, idle.Touch(now.Add(tc.elapsed)))
		})
	}
	idle, err := model.NewIdle(now, 7200)
	require.NoError(t, err)
	require.False(t, idle.SetTier(now.Add(2*time.Hour), 14400))
	require.False(t, idle.Touch(now.Add(2*time.Hour)))
}

// TestTierDowngradeKeepsLastInteractionTime prevents token refresh or plan changes from resetting the idle clock.
func TestTierDowngradeKeepsLastInteractionTime(t *testing.T) {
	now := time.Now()
	idle, err := model.NewIdle(now, 14400)
	require.NoError(t, err)
	require.True(t, idle.SetTier(now.Add(time.Hour), 7200))
	require.Equal(t, now.Add(2*time.Hour), idle.Deadline())
	require.False(t, idle.SetTier(now.Add(3*time.Hour), 14400))
}
