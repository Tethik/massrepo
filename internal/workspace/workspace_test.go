package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionLabelRoundTrip(t *testing.T) {
	workspaceName := "test-ws"
	sessionID := "20260410-120000"
	sessionDir := "/home/user/.massrepo/workspace/test-ws/sessions/" + sessionID
	image := "massrepo-claude:latest"
	created := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	containerID := "abc123def456"

	labels := labelsForSession(workspaceName, sessionID, sessionDir, image, created)
	got, err := sessionFromLabels(containerID, "running", labels)
	require.NoError(t, err)

	assert.Equal(t, workspaceName, got.WorkspaceName)
	assert.Equal(t, sessionID, got.ID)
	assert.Equal(t, sessionDir, got.SessionDir)
	assert.Equal(t, image, got.Image)
	assert.Equal(t, created.UTC(), got.Created.UTC())
	assert.Equal(t, containerID, got.Container)
	assert.Equal(t, "running", got.Status)
}

func TestSessionFromLabels_MissingLabel(t *testing.T) {
	labels := map[string]string{
		labelManaged:       "true",
		labelWorkspaceName: "test-ws",
		// labelSessionID intentionally missing
		labelSessionDir:     "/some/dir",
		labelSessionImage:   "some-image",
		labelSessionCreated: "2026-04-10T12:00:00Z",
	}

	_, err := sessionFromLabels("cid123", "running", labels)
	assert.ErrorContains(t, err, labelSessionID)
}

func TestSessionFromLabels_InvalidCreated(t *testing.T) {
	labels := map[string]string{
		labelManaged:        "true",
		labelWorkspaceName:  "test-ws",
		labelSessionID:      "20260410-120000",
		labelSessionDir:     "/some/dir",
		labelSessionImage:   "some-image",
		labelSessionCreated: "not-a-time",
	}

	_, err := sessionFromLabels("cid123", "running", labels)
	assert.ErrorContains(t, err, "invalid created label")
}

func TestContainerName(t *testing.T) {
	assert.Equal(t, "massrepo-my-workspace-20260410-120000", containerName("my-workspace", "20260410-120000"))
}

func TestWorkspaceConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := WorkspaceConfig{
		Name:    "my-ws",
		Image:   "massrepo-claude:latest",
		Created: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		WorkDir: dir,
	}

	require.NoError(t, writeWorkspaceConfig(cfg))

	got, err := readWorkspaceConfig(dir)
	require.NoError(t, err)

	assert.Equal(t, cfg.Name, got.Name)
	assert.Equal(t, cfg.Image, got.Image)
	assert.Equal(t, cfg.Created.UTC(), got.Created.UTC())
	assert.Equal(t, dir, got.WorkDir)
}

func TestWorkspaceConfigPath(t *testing.T) {
	assert.Equal(t, filepath.Join("/some/dir", "workspace.yaml"), workspaceConfigPath("/some/dir"))
}

func TestReadWorkspaceConfig_NotFound(t *testing.T) {
	_, err := readWorkspaceConfig(t.TempDir())
	assert.Error(t, err)
}

func TestReadWorkspaceConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(workspaceConfigPath(dir), []byte("not json"), 0o644))

	_, err := readWorkspaceConfig(dir)
	assert.ErrorContains(t, err, "parse workspace config")
}
