package pack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/miroslav-matejovsky/opdl/builder/deployment"
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
	_, err := New(t.TempDir(), t.TempDir(), "")
	require.ErrorContains(t, err, "find embedded deployment descriptor")
}

func TestStageOverlayLeavesPlaceholderUnchanged(t *testing.T) {
	platformDir := fakePlatform(t)
	embedFile := filepath.Join(platformDir, "embedded", deploymentFile)

	p, err := New(platformDir, t.TempDir(), "")
	require.NoError(t, err)
	overlayPath, err := p.stageOverlay(t.TempDir(), deployment.Descriptor{Project: "customer-a"})
	require.NoError(t, err)

	data, err := os.ReadFile(embedFile)
	require.NoError(t, err)
	require.JSONEq(t, `{"project":"mock"}`, string(data))

	overlay, err := os.ReadFile(overlayPath)
	require.NoError(t, err)
	var document struct {
		Replace map[string]string `json:"Replace"`
	}
	require.NoError(t, json.Unmarshal(overlay, &document))
	require.Equal(t, filepath.Join(filepath.Dir(overlayPath), deploymentFile), document.Replace[filepath.Clean(embedFile)])
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

func TestLaunches(t *testing.T) {
	primaryService := &deployment.WinService{Name: "node-primary", DisplayName: "node primary"}
	standbyService := &deployment.WinService{Name: "node-standby", DisplayName: "node standby"}

	t.Run("standby enabled", func(t *testing.T) {
		primary, standby := launches(deployment.Slots{
			Primary: deployment.Slot{Service: primaryService},
			Standby: deployment.Slot{Disabled: false, Service: standbyService},
		})
		require.Equal(t, Launch{
			Service: WinService{Name: "node-primary", DisplayName: "node primary"},
			Args:    []string{"-instance", "primary"},
		}, primary)
		require.Equal(t, &Launch{
			Service: WinService{Name: "node-standby", DisplayName: "node standby"},
			Args:    []string{"-instance", "standby"},
		}, standby)
	})

	// A machine that deploys no Standby Instance ships no standby launch, so
	// nothing names a service for an instance that will never run.
	t.Run("standby disabled", func(t *testing.T) {
		primary, standby := launches(deployment.Slots{
			Primary: deployment.Slot{Service: primaryService},
			Standby: deployment.Slot{Disabled: true},
		})
		require.Equal(t, Launch{
			Service: WinService{Name: "node-primary", DisplayName: "node primary"},
			Args:    []string{"-instance", "primary"},
		}, primary)
		require.Nil(t, standby)
	})
}
