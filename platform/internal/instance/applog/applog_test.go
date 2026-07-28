package applog_test

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/miroslav-matejovsky/opdl/platform/internal/instance/applog"
)

// record is one line of an application log, as whoever reads the file sees it.
type record struct {
	Level    string `json:"level"`
	Message  string `json:"msg"`
	Time     string `json:"time"`
	Machine  string `json:"machine"`
	Instance string `json:"instance"`
	Address  string `json:"address"`
}

// readRecords decodes every line the logger wrote to path.
func readRecords(t *testing.T, path string) []record {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var records []record
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r record
		require.NoErrorf(t, json.Unmarshal([]byte(line), &r), "log line does not decode: %s", line)
		records = append(records, r)
	}
	return records
}

func TestOpenWritesOneJSONRecordPerLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "platform.log")
	logger, err := applog.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	logger.Info("listening", "address", "127.0.0.1:8080")
	logger.Warn("standby projection unavailable")

	records := readRecords(t, path)
	require.Len(t, records, 2)
	require.Equal(t, "INFO", records[0].Level)
	require.Equal(t, "listening", records[0].Message)
	require.Equal(t, "127.0.0.1:8080", records[0].Address)
	require.NotEmpty(t, records[0].Time, "every record is timestamped")
	require.Equal(t, "WARN", records[1].Level)
}

// TestOpenStampsTheBaseAttributesOnEveryRecord checks a line taken out of
// context still says which instance wrote it, including one written through a
// logger derived from the opened one.
func TestOpenStampsTheBaseAttributesOnEveryRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "platform.log")
	logger, err := applog.Open(path,
		slog.String("machine", "node-a"),
		slog.String("instance", "standby"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	logger.Info("platform starting")
	logger.With("address", "127.0.0.1:8081").Info("listening")

	records := readRecords(t, path)
	require.Len(t, records, 2)
	for _, r := range records {
		require.Equal(t, "node-a", r.Machine)
		require.Equal(t, "standby", r.Instance)
	}
	require.Equal(t, "127.0.0.1:8081", records[1].Address)
}

// TestOpenAppendsToAnExistingLog checks a restart continues the instance's log
// rather than truncating what the previous incarnation wrote. There is no
// rotation, so this file is the whole history until something outside the
// platform moves it.
func TestOpenAppendsToAnExistingLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "platform.log")

	first, err := applog.Open(path)
	require.NoError(t, err)
	first.Info("platform starting")
	require.NoError(t, first.Close())

	second, err := applog.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	second.Info("platform starting")

	require.Len(t, readRecords(t, path), 2)
}

func TestOpenCreatesTheParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "primary", "platform.log")
	logger, err := applog.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	require.Equal(t, path, logger.Path())
	require.FileExists(t, path)
}

func TestOpenRejectsAnEmptyPath(t *testing.T) {
	_, err := applog.Open("   ")
	require.ErrorIs(t, err, applog.ErrInvalidPath)
}

// TestCloseIsIdempotent checks the process may close its log more than once,
// which a shutdown path joining several deferred closes can.
func TestCloseIsIdempotent(t *testing.T) {
	logger, err := applog.Open(filepath.Join(t.TempDir(), "platform.log"))
	require.NoError(t, err)

	require.NoError(t, logger.Close())
	require.NoError(t, logger.Close())
}

// TestLoggingAfterCloseIsDropped checks a record written by something that
// outlived the close does not panic or fail the caller. slog discards a
// handler's error, and a process already shutting down has nowhere to report a
// log write it could not make.
func TestLoggingAfterCloseIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "platform.log")
	logger, err := applog.Open(path)
	require.NoError(t, err)
	require.NoError(t, logger.Close())

	require.NotPanics(t, func() { logger.Info("too late") })
	require.Empty(t, readRecords(t, path))
}
