package workspace

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWelcomeBanner_WithServers(t *testing.T) {
	banner := welcomeBanner(WorkspaceConfig{
		Name: "demo",
		MCPServers: map[string]MCPServer{
			"sentry":  {Type: "http"},
			"grafana": {Type: "stdio"},
		},
	})

	assert.Contains(t, banner, `workspace "demo"`)
	assert.Contains(t, banner, "claude")
	assert.Contains(t, banner, "gh auth login")
	assert.Contains(t, banner, "git config")
	assert.Contains(t, banner, "/mcp")
	// Servers are named and sorted.
	assert.Contains(t, banner, "grafana, sentry")
}

func TestWelcomeBanner_NoServers(t *testing.T) {
	banner := welcomeBanner(WorkspaceConfig{Name: "demo"})
	assert.Contains(t, banner, "/mcp")
	assert.Contains(t, banner, "authenticate MCP servers")
}

func TestWelcomeShownMarker(t *testing.T) {
	workDir := t.TempDir()
	assert.False(t, welcomeShown(workDir), "no marker yet")

	require.NoError(t, markWelcomeShown(workDir))

	assert.True(t, welcomeShown(workDir), "marker present after marking")
	assert.FileExists(t, filepath.Join(workDir, welcomeMarker))
}
