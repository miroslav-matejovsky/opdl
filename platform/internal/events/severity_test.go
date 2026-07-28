package events

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeverityValidAcceptsOnlyTheClosedSet(t *testing.T) {
	tests := []struct {
		name     string
		severity Severity
		want     bool
	}{
		{name: "info", severity: SeverityInfo, want: true},
		{name: "warn", severity: SeverityWarn, want: true},
		{name: "error", severity: SeverityError, want: true},
		{name: "default is info", severity: DefaultSeverity, want: true},
		{name: "empty", severity: "", want: false},
		{name: "unknown level", severity: "fatal", want: false},
		{name: "wrong case", severity: "INFO", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.severity.Valid())
		})
	}
}
