package pack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileSHA256Stable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))
	sum, err := fileSHA256(path)
	require.NoError(t, err)
	// SHA-256 of "hello".
	require.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", sum)
}

func TestWriteJSONRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	require.NoError(t, writeJSON(path, map[string]string{"k": "v"}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "\"k\": \"v\"")
}

func TestBinaryExtByTarget(t *testing.T) {
	require.Equal(t, ".exe", New("", "", "windows", "amd64").binaryExt())
	require.Empty(t, New("", "", "linux", "amd64").binaryExt())
}
