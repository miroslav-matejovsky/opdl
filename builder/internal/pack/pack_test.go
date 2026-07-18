package pack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakePlatform creates a platform-shaped temp dir with an embedded deployment
// descriptor holding a placeholder, and returns the platform dir.
func fakePlatform(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	embed := filepath.Join(root, "embedded")
	require.NoError(t, os.MkdirAll(embed, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(embed, deploymentFile), []byte(`{"project":"mock"}`), 0o644))
	return root
}

func TestNewFailsWithoutEmbeddedDescriptor(t *testing.T) {
	_, err := New(t.TempDir(), t.TempDir(), "", "")
	require.ErrorContains(t, err, "snapshot embedded deployment descriptor")
}

func TestRestoreReinstatesPlaceholder(t *testing.T) {
	platformDir := fakePlatform(t)
	embedFile := filepath.Join(platformDir, "embedded", deploymentFile)

	p, err := New(platformDir, t.TempDir(), "", "")
	require.NoError(t, err)

	// Simulate staging: overwrite the placeholder with a machine descriptor.
	require.NoError(t, os.WriteFile(embedFile, []byte(`{"project":"customer-a"}`), 0o644))

	require.NoError(t, p.Restore())

	data, err := os.ReadFile(embedFile)
	require.NoError(t, err)
	require.JSONEq(t, `{"project":"mock"}`, string(data))
}

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
	require.Equal(t, ".exe", (&Packer{goos: "windows"}).binaryExt())
	require.Empty(t, (&Packer{goos: "linux"}).binaryExt())
}

func TestLaunches(t *testing.T) {
	t.Run("warm standby enabled", func(t *testing.T) {
		primary, standby := launches(true)
		require.Equal(t, Launch{Args: []string{"-instance", "primary"}}, primary)
		require.Equal(t, &Launch{Args: []string{"-instance", "standby"}}, standby)
	})

	t.Run("warm standby disabled", func(t *testing.T) {
		primary, standby := launches(false)
		require.Equal(t, Launch{Args: []string{"-instance", "primary"}}, primary)
		require.Nil(t, standby)
	})
}
