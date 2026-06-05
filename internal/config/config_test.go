package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tethik/massrepo/internal/workspace"
)

func TestLoad_SkillsAndMCPServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".config", "massrepo", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))

	content := `
skills:
  - ~/skills/triage
  - git: https://github.com/org/repo
    ref: v1.2.0
    subdir: triage
mcp_servers:
  sentry:
    type: http
    url: https://mcp.sentry.dev/mcp
  local-tool:
    type: stdio
    command: /usr/bin/tool
    args: ["--flag"]
default_mcp_servers: [sentry]
`
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o644))

	cfg, err := Load()
	require.NoError(t, err)

	// Scalar skill decodes into Path; mapping decodes into git fields.
	require.Len(t, cfg.Skills, 2)
	assert.Equal(t, workspace.SkillSource{Path: "~/skills/triage"}, cfg.Skills[0])
	assert.Equal(t, workspace.SkillSource{
		Git: "https://github.com/org/repo", Ref: "v1.2.0", Subdir: "triage",
	}, cfg.Skills[1])

	assert.Equal(t, []string{"sentry"}, cfg.DefaultMCPServers)
	assert.Equal(t, "https://mcp.sentry.dev/mcp", cfg.MCPServers["sentry"].URL)
	assert.Equal(t, "/usr/bin/tool", cfg.MCPServers["local-tool"].Command)
	assert.Equal(t, []string{"--flag"}, cfg.MCPServers["local-tool"].Args)
}
