package workspace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceLabelRoundTrip(t *testing.T) {
	original := Workspace{
		Name:      "test-ws",
		Repos:     []string{"einride/security-infra", "einride/incidentio-api-poller"},
		WorkDir:   "/home/user/.massrepo/workspaces/test-ws",
		Image:     "massrepo-claude:latest",
		Created:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		Container: "abc123def456",
		Status:    "running",
	}

	labels := labelsForWorkspace(original)

	got, err := workspaceFromLabels(original.Container, original.Status, labels)
	require.NoError(t, err)

	assert.Equal(t, original.Name, got.Name)
	assert.Equal(t, original.Repos, got.Repos)
	assert.Equal(t, original.WorkDir, got.WorkDir)
	assert.Equal(t, original.Image, got.Image)
	assert.Equal(t, original.Created.UTC(), got.Created.UTC())
	assert.Equal(t, original.Container, got.Container)
	assert.Equal(t, original.Status, got.Status)
}

func TestWorkspaceFromLabels_MissingLabel(t *testing.T) {
	labels := map[string]string{
		labelManaged: "true",
		labelName:    "test-ws",
		// labelRepos intentionally missing
		labelWorkDir: "/some/dir",
		labelImage:   "some-image",
		labelCreated: "2026-04-10T12:00:00Z",
	}

	_, err := workspaceFromLabels("cid123", "running", labels)
	assert.ErrorContains(t, err, labelRepos)
}

func TestWorkspaceFromLabels_InvalidCreated(t *testing.T) {
	labels := map[string]string{
		labelManaged: "true",
		labelName:    "test-ws",
		labelRepos:   "einride/security-infra",
		labelWorkDir: "/some/dir",
		labelImage:   "some-image",
		labelCreated: "not-a-time",
	}

	_, err := workspaceFromLabels("cid123", "running", labels)
	assert.ErrorContains(t, err, "invalid created label")
}

func TestContainerName(t *testing.T) {
	assert.Equal(t, "massrepo-my-workspace", containerName("my-workspace"))
}
