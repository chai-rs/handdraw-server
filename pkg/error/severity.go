package errx

import (
	"fmt"
	"strings"
)

// Severity ranks how serious an error is, from informational to critical.
type Severity uint8

// The supported severity levels, ordered from least to most severe.
const (
	SeverityInfo Severity = iota
	SeverityLow
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityLow:
		return "low"
	case SeverityMedium:
		return "medium"
	case SeverityHigh:
		return "high"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// MarshalText implements encoding.TextMarshaler for Severity.
func (s Severity) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler for Severity.
func (s *Severity) UnmarshalText(data []byte) error {
	parsed, err := ParseSeverity(string(data))
	if err != nil {
		return err
	}

	*s = parsed

	return nil
}

// ParseSeverity maps a severity name ("info", "low", "medium", "high",
// "critical"; case-insensitive and surrounding whitespace trimmed) to the typed
// Severity. Unknown input returns an error.
func ParseSeverity(text string) (Severity, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "info":
		return SeverityInfo, nil
	case "low":
		return SeverityLow, nil
	case "medium":
		return SeverityMedium, nil
	case "high":
		return SeverityHigh, nil
	case "critical":
		return SeverityCritical, nil
	default:
		return 0, fmt.Errorf("errx: unknown severity %q", text)
	}
}
