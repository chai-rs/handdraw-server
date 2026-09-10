package errx_test

import (
	"encoding/json"
	"testing"

	errx "github.com/chai-rs/handdraw-server/pkg/error"
	"github.com/stretchr/testify/require"
)

func TestSeverityString(t *testing.T) {
	for _, tc := range []struct {
		severity errx.Severity
		want     string
	}{
		{errx.SeverityInfo, "info"},
		{errx.SeverityLow, "low"},
		{errx.SeverityMedium, "medium"},
		{errx.SeverityHigh, "high"},
		{errx.SeverityCritical, "critical"},
		{errx.Severity(99), "unknown"},
	} {
		t.Run(tc.want, func(t *testing.T) { require.Equal(t, tc.want, tc.severity.String()) })
	}
}

func TestSeverityOrdering(t *testing.T) {
	require.Less(t, errx.SeverityInfo, errx.SeverityLow)
	require.Less(t, errx.SeverityLow, errx.SeverityMedium)
	require.Less(t, errx.SeverityMedium, errx.SeverityHigh)
	require.Less(t, errx.SeverityHigh, errx.SeverityCritical)
}

func TestSeverityMarshalText(t *testing.T) {
	for _, severity := range []errx.Severity{errx.SeverityInfo, errx.SeverityLow, errx.SeverityMedium, errx.SeverityHigh, errx.SeverityCritical} {
		t.Run(severity.String(), func(t *testing.T) {
			data, err := severity.MarshalText()
			require.NoError(t, err)
			var got errx.Severity
			require.NoError(t, got.UnmarshalText(data))
			require.Equal(t, severity, got)
		})
	}
}

func TestSeverityJSONRoundTrip(t *testing.T) {
	type wrapper struct {
		Severity errx.Severity `json:"sev"`
	}
	original := wrapper{Severity: errx.SeverityHigh}
	data, err := json.Marshal(original)
	require.NoError(t, err)
	require.Equal(t, `{"sev":"high"}`, string(data))
	var got wrapper
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, original, got)
}

func TestParseSeverityCaseInsensitive(t *testing.T) {
	got, err := errx.ParseSeverity("  HIGH ")
	require.NoError(t, err)
	require.Equal(t, errx.SeverityHigh, got)
}

func TestParseSeverityUnknown(t *testing.T) {
	_, err := errx.ParseSeverity("nope")
	require.Error(t, err)
}
