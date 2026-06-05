package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// welcomeMarker is the sentinel file (in the workspace directory, outside the
// container bind mounts) recording that the first-session welcome has been shown.
const welcomeMarker = ".welcomed"

// welcomeBanner builds the one-time setup reminder shown on a workspace's first
// session. It lists the tools whose auth lives in the shared workspace home and,
// when MCP servers are configured, names them in the /mcp line.
func welcomeBanner(cfg WorkspaceConfig) string {
	mcpLine := "inside claude, run `/mcp` to authenticate MCP servers"
	if len(cfg.MCPServers) > 0 {
		names := make([]string, 0, len(cfg.MCPServers))
		for name := range cfg.MCPServers {
			names = append(names, name)
		}
		sort.Strings(names)
		mcpLine = fmt.Sprintf("inside claude, run `/mcp` to authenticate: %s", strings.Join(names, ", "))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n👋 Welcome to workspace %q — looks like your first session here.\n\n", cfg.Name)
	b.WriteString("Auth lives in $HOME and is shared across all sessions of this workspace,\n")
	b.WriteString("so you only need to set this up once:\n\n")
	b.WriteString("  • claude  — run `claude`, then `/login`\n")
	b.WriteString("  • gh      — run `gh auth login`\n")
	b.WriteString("  • git     — set your identity: git config --global user.name / user.email\n")
	fmt.Fprintf(&b, "  • /mcp    — %s\n\n", mcpLine)
	return b.String()
}

// welcomeShown reports whether the first-session welcome has already been shown
// for the workspace at workDir.
func welcomeShown(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, welcomeMarker))
	return err == nil
}

// markWelcomeShown records that the welcome has been shown for the workspace.
func markWelcomeShown(workDir string) error {
	content := time.Now().UTC().Format(time.RFC3339) + "\n"
	return os.WriteFile(filepath.Join(workDir, welcomeMarker), []byte(content), 0o644)
}
