package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// newTestWorkspace builds a Manager backed by a temp dir and a single workspace
// with its home directory, returning the manager and the workspace name.
func newTestWorkspace(t *testing.T) (*Manager, string) {
	t.Helper()
	dataDir := t.TempDir()
	wsParent := filepath.Join(dataDir, "workspace")
	m := &Manager{
		workspacesDir: wsParent,
		skillCacheDir: filepath.Join(dataDir, "skillcache"),
	}
	const name = "demo"
	workDir := filepath.Join(wsParent, name)
	require.NoError(t, createWorkspaceDirs(workDir))
	require.NoError(t, writeWorkspaceConfig(WorkspaceConfig{Name: name, Image: "img", WorkDir: workDir}))
	return m, name
}

func TestAddAndRemoveSkill(t *testing.T) {
	m, ws := newTestWorkspace(t)

	// A local skill source directory.
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("# skill"), 0o644))
	src := SkillSource{Path: srcDir}
	name := SkillName(src)

	require.NoError(t, m.AddSkill(context.Background(), ws, src))

	// Materialized into the workspace home and recorded in the config.
	installed := filepath.Join(m.workspacesDir, ws, "home", ".claude", "skills", name, "SKILL.md")
	assert.FileExists(t, installed)
	cfg, err := m.Workspace(ws)
	require.NoError(t, err)
	require.Len(t, cfg.Skills, 1)
	assert.Equal(t, src, cfg.Skills[0])

	// Adding the same name again replaces rather than duplicates.
	require.NoError(t, m.AddSkill(context.Background(), ws, src))
	cfg, err = m.Workspace(ws)
	require.NoError(t, err)
	assert.Len(t, cfg.Skills, 1)

	require.NoError(t, m.RemoveSkill(ws, name))
	assert.NoFileExists(t, installed)
	cfg, err = m.Workspace(ws)
	require.NoError(t, err)
	assert.Empty(t, cfg.Skills)
}

func TestSeedSkillStripsGitDir(t *testing.T) {
	m, ws := newTestWorkspace(t)

	// A source that is a repo root: SKILL.md alongside a .git tree.
	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("# skill"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(srcDir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, ".git", "HEAD"), []byte("ref: x"), 0o644))

	src := SkillSource{Path: srcDir}
	require.NoError(t, m.AddSkill(context.Background(), ws, src))

	base := filepath.Join(m.workspacesDir, ws, "home", ".claude", "skills", SkillName(src))
	assert.FileExists(t, filepath.Join(base, "SKILL.md"))
	assert.NoDirExists(t, filepath.Join(base, ".git"))
}

func TestAddAndRemoveMCPServer(t *testing.T) {
	m, ws := newTestWorkspace(t)
	srv := MCPServer{Type: "http", URL: "https://mcp.sentry.dev/mcp"}

	require.NoError(t, m.AddMCPServer(ws, "sentry", srv))

	claudePath := filepath.Join(m.workspacesDir, ws, "home", ".claude.json")
	data, err := os.ReadFile(claudePath)
	require.NoError(t, err)
	var root map[string]any
	require.NoError(t, json.Unmarshal(data, &root))
	assert.Contains(t, root["mcpServers"].(map[string]any), "sentry")

	cfg, err := m.Workspace(ws)
	require.NoError(t, err)
	assert.Equal(t, srv, cfg.MCPServers["sentry"])

	require.NoError(t, m.RemoveMCPServer(ws, "sentry"))

	data, err = os.ReadFile(claudePath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &root))
	assert.NotContains(t, root["mcpServers"].(map[string]any), "sentry")
	cfg, err = m.Workspace(ws)
	require.NoError(t, err)
	assert.Empty(t, cfg.MCPServers)
}

func TestMergeMCPServers_PreservesExistingKeys(t *testing.T) {
	workDir := t.TempDir()
	homeDir := filepath.Join(workDir, "home")
	require.NoError(t, os.MkdirAll(homeDir, 0o755))
	// A pre-existing .claude.json with auth-like state and a manually added server.
	existing := `{
  "oauthAccount": {"emailAddress": "me@example.com"},
  "mcpServers": {"manual": {"type": "http", "url": "https://manual.example"}}
}`
	claudePath := filepath.Join(homeDir, ".claude.json")
	require.NoError(t, os.WriteFile(claudePath, []byte(existing), 0o644))

	err := mergeMCPServers(workDir, map[string]MCPServer{
		"sentry": {Type: "http", URL: "https://mcp.sentry.dev/mcp"},
	})
	require.NoError(t, err)

	data, err := os.ReadFile(claudePath)
	require.NoError(t, err)
	var root map[string]any
	require.NoError(t, json.Unmarshal(data, &root))

	// Unrelated top-level key survives.
	assert.Contains(t, root, "oauthAccount")

	mcp, ok := root["mcpServers"].(map[string]any)
	require.True(t, ok)
	// Both the pre-existing and the new server are present.
	assert.Contains(t, mcp, "manual")
	assert.Contains(t, mcp, "sentry")
	sentry := mcp["sentry"].(map[string]any)
	assert.Equal(t, "https://mcp.sentry.dev/mcp", sentry["url"])
}

func TestMergeMCPServers_CreatesFileWhenAbsent(t *testing.T) {
	workDir := t.TempDir()
	err := mergeMCPServers(workDir, map[string]MCPServer{
		"tool": {Type: "stdio", Command: "/usr/bin/tool", Args: []string{"--x"}},
	})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(workDir, "home", ".claude.json"))
	require.NoError(t, err)
	var root map[string]any
	require.NoError(t, json.Unmarshal(data, &root))
	mcp := root["mcpServers"].(map[string]any)
	tool := mcp["tool"].(map[string]any)
	assert.Equal(t, "stdio", tool["type"])
	assert.Equal(t, "/usr/bin/tool", tool["command"])
}

func TestMergeMCPServers_NoServersIsNoop(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, mergeMCPServers(workDir, nil))
	_, err := os.Stat(filepath.Join(workDir, "home", ".claude.json"))
	assert.True(t, os.IsNotExist(err), "no file should be created for empty server set")
}

func TestSkillSourceUnmarshalYAML(t *testing.T) {
	for _, tt := range []struct {
		name string
		yaml string
		want SkillSource
	}{
		{name: "scalar path", yaml: `~/skills/triage`, want: SkillSource{Path: "~/skills/triage"}},
		{
			name: "git mapping",
			yaml: "git: https://github.com/org/repo\nref: v1.2.0\nsubdir: triage",
			want: SkillSource{Git: "https://github.com/org/repo", Ref: "v1.2.0", Subdir: "triage"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got SkillSource
			require.NoError(t, yaml.Unmarshal([]byte(tt.yaml), &got))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSkillSourceMarshalYAML_RoundTrip(t *testing.T) {
	for _, tt := range []struct {
		name string
		src  SkillSource
	}{
		{name: "path", src: SkillSource{Path: "/abs/skill"}},
		{name: "git", src: SkillSource{Git: "https://github.com/org/repo", Ref: "main", Subdir: "s"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := yaml.Marshal(tt.src)
			require.NoError(t, err)
			var got SkillSource
			require.NoError(t, yaml.Unmarshal(data, &got))
			assert.Equal(t, tt.src, got)
		})
	}
}

func TestStripSecrets(t *testing.T) {
	in := MCPServer{
		Type:    "http",
		URL:     "https://mcp.example",
		Headers: map[string]string{"Authorization": "Bearer secret"},
		Env:     map[string]string{"TOKEN": "abc123"},
	}
	got := stripSecrets(in)

	// Keys preserved, values blanked.
	assert.Equal(t, "", got.Headers["Authorization"])
	assert.Equal(t, "", got.Env["TOKEN"])
	assert.Contains(t, got.Headers, "Authorization")
	assert.Contains(t, got.Env, "TOKEN")
	// Non-secret fields untouched and the input is not mutated.
	assert.Equal(t, "https://mcp.example", got.URL)
	assert.Equal(t, "Bearer secret", in.Headers["Authorization"])
}

func TestBuildManifest_SkipsLocalSkillsAndStrips(t *testing.T) {
	cfg := WorkspaceConfig{
		Image: "massrepo-claude:latest",
		Skills: []SkillSource{
			{Path: "/local/only"},
			{Git: "https://github.com/org/repo", Ref: "v1"},
		},
		MCPServers: map[string]MCPServer{
			"sentry": {Type: "http", URL: "https://mcp.sentry.dev", Headers: map[string]string{"Authorization": "Bearer t"}},
		},
	}
	man := buildManifest(cfg)

	require.Len(t, man.Skills, 1)
	assert.Equal(t, "https://github.com/org/repo", man.Skills[0].Git)
	assert.Equal(t, "", man.MCPServers["sentry"].Headers["Authorization"])
}

func TestSkillName(t *testing.T) {
	for _, tt := range []struct {
		name string
		src  SkillSource
		want string
	}{
		{name: "subdir wins", src: SkillSource{Git: "https://x/repo.git", Subdir: "skills/triage"}, want: "triage"},
		{name: "git repo basename", src: SkillSource{Git: "https://github.com/org/my-skill.git"}, want: "my-skill"},
		{name: "subdir dot uses repo name", src: SkillSource{Git: "https://github.com/org/my-skill.git", Subdir: "."}, want: "my-skill"},
		{name: "local path basename", src: SkillSource{Path: "/a/b/repo-triage"}, want: "repo-triage"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SkillName(tt.src))
		})
	}
}

func TestSkillSourceValidate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		src     SkillSource
		wantErr string
	}{
		{name: "local path", src: SkillSource{Path: "/a/b"}},
		{name: "git with ref and subdir", src: SkillSource{Git: "https://x/r", Ref: "v1", Subdir: "s"}},
		{name: "git missing subdir", src: SkillSource{Git: "https://x/r", Ref: "v1"}, wantErr: "subdir"},
		{name: "git missing ref", src: SkillSource{Git: "https://x/r", Subdir: "s"}, wantErr: "ref"},
		{name: "neither", src: SkillSource{}, wantErr: "either path or git"},
		{name: "both", src: SkillSource{Path: "/a", Git: "https://x/r"}, wantErr: "not both"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.src.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestFillBlanks(t *testing.T) {
	got := fillBlanks(
		map[string]string{"TOKEN": "", "KEEP": "new", "MISSING": ""},
		map[string]string{"TOKEN": "secret", "KEEP": "old"},
	)
	assert.Equal(t, "secret", got["TOKEN"], "blank value filled from existing")
	assert.Equal(t, "new", got["KEEP"], "non-blank value kept")
	assert.Equal(t, "", got["MISSING"], "blank with no existing stays blank")
	assert.Nil(t, fillBlanks(nil, map[string]string{"X": "y"}))
}

// makeSkillDir creates a directory with a SKILL.md and returns it.
func makeSkillDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+name), 0o644))
	return dir
}

func TestApplyManifest_AdditivePreservesSecrets(t *testing.T) {
	m, ws := newTestWorkspace(t)
	skillX := makeSkillDir(t, "skill-x")
	skillY := makeSkillDir(t, "skill-y")

	// Pre-existing state: a server with a real secret, plus one skill.
	require.NoError(t, m.AddMCPServer(ws, "sentry", MCPServer{
		Type: "http", URL: "https://mcp.sentry.dev", Headers: map[string]string{"Authorization": "realsecret"},
	}))
	require.NoError(t, m.AddSkill(context.Background(), ws, SkillSource{Path: skillX}))

	// Apply a manifest: sentry with a blanked secret, a new server, a new skill.
	man := Manifest{
		Skills: []SkillSource{{Path: skillY}},
		MCPServers: map[string]MCPServer{
			"sentry":  {Type: "http", URL: "https://mcp.sentry.dev", Headers: map[string]string{"Authorization": ""}},
			"grafana": {Type: "stdio", Command: "/usr/bin/grafana"},
		},
	}
	require.NoError(t, m.ApplyManifest(context.Background(), ws, man, ApplyOptions{}))

	workDir := filepath.Join(m.workspacesDir, ws)
	servers, err := existingMCPServers(workDir)
	require.NoError(t, err)
	assert.Equal(t, "realsecret", servers["sentry"].Headers["Authorization"], "secret preserved in .claude.json")
	assert.Contains(t, servers, "grafana", "new server added")

	cfg, err := m.Workspace(ws)
	require.NoError(t, err)
	assert.Equal(t, "realsecret", cfg.MCPServers["sentry"].Headers["Authorization"])
	assert.Contains(t, cfg.MCPServers, "grafana")

	// Both skills remain installed and recorded.
	assert.FileExists(t, filepath.Join(workDir, "home", ".claude", "skills", "skill-x", "SKILL.md"))
	assert.FileExists(t, filepath.Join(workDir, "home", ".claude", "skills", "skill-y", "SKILL.md"))
	assert.Len(t, cfg.Skills, 2)
}

func TestApplyManifest_Prune(t *testing.T) {
	m, ws := newTestWorkspace(t)
	skillX := makeSkillDir(t, "skill-x")
	skillY := makeSkillDir(t, "skill-y")

	require.NoError(t, m.AddMCPServer(ws, "sentry", MCPServer{Type: "http", URL: "https://x"}))
	require.NoError(t, m.AddSkill(context.Background(), ws, SkillSource{Path: skillX}))

	man := Manifest{
		Skills:     []SkillSource{{Path: skillY}},
		MCPServers: map[string]MCPServer{"grafana": {Type: "stdio", Command: "/g"}},
	}
	require.NoError(t, m.ApplyManifest(context.Background(), ws, man, ApplyOptions{Prune: true}))

	workDir := filepath.Join(m.workspacesDir, ws)
	servers, err := existingMCPServers(workDir)
	require.NoError(t, err)
	assert.NotContains(t, servers, "sentry", "server absent from manifest is pruned")
	assert.Contains(t, servers, "grafana")

	cfg, err := m.Workspace(ws)
	require.NoError(t, err)
	assert.NotContains(t, cfg.MCPServers, "sentry")
	assert.NoDirExists(t, filepath.Join(workDir, "home", ".claude", "skills", "skill-x"))
	assert.FileExists(t, filepath.Join(workDir, "home", ".claude", "skills", "skill-y", "SKILL.md"))
	require.Len(t, cfg.Skills, 1)
	assert.Equal(t, skillY, cfg.Skills[0].Path)
}

func TestWriteAndReadManifest(t *testing.T) {
	man := Manifest{
		Image:  "massrepo-claude:latest",
		Skills: []SkillSource{{Git: "https://github.com/org/repo", Ref: "v1.2.0", Subdir: "triage"}},
		MCPServers: map[string]MCPServer{
			"sentry": {Type: "http", URL: "https://mcp.sentry.dev"},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, WriteManifest(man, &buf))

	path := filepath.Join(t.TempDir(), "massrepo.yaml")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))

	got, err := ReadManifest(path)
	require.NoError(t, err)
	assert.Equal(t, man, got)
}
