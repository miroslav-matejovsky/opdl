package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// file is the schema of the platform's TOML configuration file. It carries the
// settings a user may set without touching the embedded deployment descriptor.
// Neither the API address nor the runtime directory is here. Both are an
// instance's own, both are resolved onto the instance's descriptor record, and a
// file that still sets either fails at rejectUnknownKeys rather than being
// quietly ignored.
type file struct {
	ReadHeaderTimeout string `toml:"read_header_timeout"`
	ShutdownTimeout   string `toml:"shutdown_timeout"`
	// LagBound bounds how long a process's projection may lag the journal before it
	// stops being ready to take over, and before an active process stops serving rather than
	// answering from a stale view. It is required and must be positive.
	LagBound string `toml:"lag_bound"`
}

// loadFile reads and validates the TOML configuration file. No defaults are
// allowed: a file that does not exist or omits required fields yields an error.
func loadFile(path string) (file, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return file{}, fmt.Errorf("read configuration file %s: file does not exist", path)
	}
	if err != nil {
		return file{}, fmt.Errorf("read configuration file %s: %w", path, err)
	}
	var f file
	meta, err := toml.Decode(string(data), &f)
	if err != nil {
		return file{}, fmt.Errorf("invalid configuration file %s: %w", path, err)
	}
	if err := rejectUnknownKeys(path, meta); err != nil {
		return file{}, err
	}
	f.ReadHeaderTimeout = strings.TrimSpace(f.ReadHeaderTimeout)
	if f.ReadHeaderTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: read_header_timeout is required", path)
	}
	f.ShutdownTimeout = strings.TrimSpace(f.ShutdownTimeout)
	if f.ShutdownTimeout == "" {
		return file{}, fmt.Errorf("configuration file %s: shutdown_timeout is required", path)
	}
	// lag_bound is required only of a deployment that has a journal, so whether it
	// may be empty is not knowable from the file alone. Load decides it against the
	// descriptor; see eventStorageSettings.
	f.LagBound = strings.TrimSpace(f.LagBound)
	return f, nil
}

// rejectUnknownKeys fails a configuration file that sets a key this schema does
// not define.
func rejectUnknownKeys(path string, meta toml.MetaData) error {
	undecoded := meta.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	keys := make([]string, 0, len(undecoded))
	for _, key := range undecoded {
		keys = append(keys, key.String())
	}
	return fmt.Errorf("configuration file %s: unknown key(s): %s", path, strings.Join(keys, ", "))
}
