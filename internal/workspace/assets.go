package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ClaudeAssets is the set of skills and MCP servers to provision into a
// workspace's persistent Claude home at creation time.
type ClaudeAssets struct {
	Skills     []SkillSource
	MCPServers map[string]MCPServer
}

// Manifest is a portable, shareable workspace setup. Skills are limited to
// git-backed sources so they resolve on any machine, and MCP secret values are
// stripped on export. See ExportManifest and ReadManifest.
type Manifest struct {
	Image      string               `yaml:"image,omitempty"`
	Skills     []SkillSource        `yaml:"skills,omitempty"`
	MCPServers map[string]MCPServer `yaml:"mcp_servers,omitempty"`
}

// seedClaudeAssets provisions skills and MCP servers into the workspace's
// persistent home so every session of the workspace inherits them. Skills are
// copied into home/.claude/skills/<name> and MCP servers are merged into
// home/.claude.json.
func (m *Manager) seedClaudeAssets(ctx context.Context, workDir string, a ClaudeAssets) error {
	if err := m.seedSkills(ctx, workDir, a.Skills); err != nil {
		return err
	}
	return mergeMCPServers(workDir, a.MCPServers)
}

// seedSkills resolves each skill source (local dir or git clone) and copies it
// into the workspace's home/.claude/skills directory.
func (m *Manager) seedSkills(ctx context.Context, workDir string, skills []SkillSource) error {
	if len(skills) == 0 {
		return nil
	}
	skillsDir := filepath.Join(workDir, "home", ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return fmt.Errorf("create skills dir: %v", err)
	}
	for _, src := range skills {
		from, err := m.resolveSkill(ctx, src)
		if err != nil {
			return err
		}
		name := SkillName(src)
		if name == "" || name == "." || name == string(filepath.Separator) {
			return fmt.Errorf("cannot determine skill name for %+v", src)
		}
		fmt.Printf("Seeding skill %q...\n", name)
		dst := filepath.Join(skillsDir, name)
		if err := copyDir(from, dst); err != nil {
			return fmt.Errorf("copy skill %q: %v", name, err)
		}
		// A skill seeded from a repo root (subdir ".") carries the repo's .git
		// tree, which has no place in an installed skill; drop it.
		if err := os.RemoveAll(filepath.Join(dst, ".git")); err != nil {
			return fmt.Errorf("prune .git from skill %q: %v", name, err)
		}
	}
	return nil
}

// resolveSkill returns a local directory for a skill source, cloning git-backed
// sources into the skill cache on demand.
func (m *Manager) resolveSkill(ctx context.Context, src SkillSource) (string, error) {
	if err := src.Validate(); err != nil {
		return "", err
	}
	if src.Path != "" {
		p := expandHome(src.Path)
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("skill path %q: %v", src.Path, err)
		}
		return p, nil
	}
	return fetchGitSkill(ctx, m.skillCacheDir, src)
}

// fetchGitSkill clones a git-backed skill into the cache (keyed by repo+ref) and
// returns the directory holding the skill (the optional subdir within the repo).
// Cached clones are reused as-is.
func fetchGitSkill(ctx context.Context, cacheDir string, src SkillSource) (string, error) {
	if src.Ref == "" {
		return "", fmt.Errorf("git skill %q: ref is required", src.Git)
	}
	key := sha256.Sum256([]byte(src.Git + "@" + src.Ref))
	dest := filepath.Join(cacheDir, hex.EncodeToString(key[:])[:16])
	if _, err := os.Stat(dest); err == nil {
		return skillSubdir(dest, src.Subdir), nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("create skill cache dir: %v", err)
	}
	fmt.Printf("Cloning skill %s@%s...\n", src.Git, src.Ref)
	// A shallow clone of the ref handles branches and tags directly.
	_, err := runWithRateLimitRetry(ctx, src.Git, "git clone", func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", src.Ref, src.Git, dest)
	})
	if err != nil {
		// --branch rejects commit SHAs; fall back to a full clone plus checkout.
		_ = os.RemoveAll(dest)
		if fallbackErr := cloneAndCheckout(ctx, src, dest); fallbackErr != nil {
			_ = os.RemoveAll(dest)
			return "", fmt.Errorf("clone git skill %q: %v", src.Git, err)
		}
	}
	return skillSubdir(dest, src.Subdir), nil
}

// cloneAndCheckout clones the full repository and checks out an arbitrary ref
// (e.g. a commit SHA that shallow --branch cloning cannot target).
func cloneAndCheckout(ctx context.Context, src SkillSource, dest string) error {
	if _, err := runWithRateLimitRetry(ctx, src.Git, "git clone", func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "git", "clone", src.Git, dest)
	}); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, "git", "-C", dest, "checkout", src.Ref).Run(); err != nil {
		return fmt.Errorf("checkout %q: %v", src.Ref, err)
	}
	return nil
}

// skillSubdir joins an optional subdir onto a cloned repo directory.
func skillSubdir(repoDir, subdir string) string {
	if subdir == "" {
		return repoDir
	}
	return filepath.Join(repoDir, filepath.FromSlash(subdir))
}

// SkillName derives the directory name a skill is installed under, preferring a
// named subdir, then the git repo name, then the local path basename. A subdir
// of "." (the repo root) falls through to the repo name.
func SkillName(src SkillSource) string {
	switch {
	case src.Subdir != "" && filepath.Clean(src.Subdir) != ".":
		return filepath.Base(filepath.FromSlash(src.Subdir))
	case src.Git != "":
		return strings.TrimSuffix(filepath.Base(src.Git), ".git")
	default:
		return filepath.Base(expandHome(src.Path))
	}
}

// expandHome expands a leading ~ to the user's home directory.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, ok := os.LookupEnv("HOME")
	if !ok {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
}

// mergeMCPServers merges the given MCP servers into the workspace's
// home/.claude.json under the top-level mcpServers key. Existing keys (auth,
// projects, manually added servers) are preserved; only the named servers are
// added or overwritten.
func mergeMCPServers(workDir string, servers map[string]MCPServer) error {
	if len(servers) == 0 {
		return nil
	}
	path := filepath.Join(workDir, "home", ".claude.json")
	root := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil && len(data) > 0:
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse .claude.json: %v", err)
		}
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("read .claude.json: %v", err)
	}

	mcp, _ := root["mcpServers"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	for name, server := range servers {
		mcp[name] = server
	}
	root["mcpServers"] = mcp

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode .claude.json: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create home dir: %v", err)
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// ExportManifest builds a portable manifest from a workspace's recorded setup.
// Local-path skills are skipped with a warning (they are not portable) and MCP
// secret values are stripped.
func (m *Manager) ExportManifest(workspaceName string) (Manifest, error) {
	cfg, err := m.Workspace(workspaceName)
	if err != nil {
		return Manifest{}, err
	}
	return buildManifest(cfg), nil
}

// buildManifest derives a portable manifest from a workspace config, skipping
// non-portable local-path skills (with a warning) and stripping MCP secrets.
func buildManifest(cfg WorkspaceConfig) Manifest {
	man := Manifest{Image: cfg.Image}
	for _, s := range cfg.Skills {
		if s.Git == "" {
			fmt.Fprintf(os.Stderr,
				"warning: skipping local-path skill %q; add it as a git source to share it\n", s.Path)
			continue
		}
		man.Skills = append(man.Skills, s)
	}
	if len(cfg.MCPServers) > 0 {
		man.MCPServers = make(map[string]MCPServer, len(cfg.MCPServers))
		for name, srv := range cfg.MCPServers {
			man.MCPServers[name] = stripSecrets(srv)
		}
	}
	return man
}

// stripSecrets blanks the values of Env and Headers while preserving their keys
// as placeholders for the importer to fill in.
func stripSecrets(s MCPServer) MCPServer {
	out := s
	if len(s.Env) > 0 {
		out.Env = make(map[string]string, len(s.Env))
		for k := range s.Env {
			out.Env[k] = ""
		}
	}
	if len(s.Headers) > 0 {
		out.Headers = make(map[string]string, len(s.Headers))
		for k := range s.Headers {
			out.Headers[k] = ""
		}
	}
	return out
}

// WriteManifest encodes a manifest as YAML to w.
func WriteManifest(man Manifest, w io.Writer) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(man); err != nil {
		return fmt.Errorf("encode manifest: %v", err)
	}
	return enc.Close()
}

// ReadManifest loads a manifest from a YAML file.
func ReadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %v", err)
	}
	var man Manifest
	if err := yaml.Unmarshal(data, &man); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %v", err)
	}
	return man, nil
}

// ApplyOptions controls how a manifest is applied to an existing workspace.
type ApplyOptions struct {
	// Prune removes skills and MCP servers not present in the manifest.
	Prune bool
}

// ApplyManifest updates an existing workspace from a manifest. By default it is
// additive: skills and MCP servers named in the manifest are added or updated
// and everything else is left in place. With ApplyOptions.Prune, items absent
// from the manifest are removed. The manifest's image is adopted. Blank
// env/header values in the manifest do not overwrite values already configured
// in the workspace, so re-applying an exported (secret-stripped) manifest keeps
// previously filled-in secrets intact.
func (m *Manager) ApplyManifest(ctx context.Context, workspaceName string, man Manifest, opts ApplyOptions) error {
	cfg, err := m.Workspace(workspaceName)
	if err != nil {
		return err
	}

	// Adopt the manifest's image, building/pulling it if necessary.
	if man.Image != "" && man.Image != cfg.Image {
		if err := m.ensureImage(ctx, man.Image); err != nil {
			return err
		}
		cfg.Image = man.Image
	}

	// Skills: materialize and upsert by installed name.
	if err := m.seedSkills(ctx, cfg.WorkDir, man.Skills); err != nil {
		return err
	}
	for _, s := range man.Skills {
		cfg.Skills = append(filterSkills(cfg.Skills, SkillName(s)), s)
	}

	// MCP servers: merge, preserving secrets already present in .claude.json.
	existing, err := existingMCPServers(cfg.WorkDir)
	if err != nil {
		return err
	}
	merged := make(map[string]MCPServer, len(man.MCPServers))
	for name, srv := range man.MCPServers {
		srv = mergeServerSecrets(existing[name], srv)
		merged[name] = srv
		if cfg.MCPServers == nil {
			cfg.MCPServers = map[string]MCPServer{}
		}
		cfg.MCPServers[name] = srv
	}
	if err := mergeMCPServers(cfg.WorkDir, merged); err != nil {
		return err
	}

	if opts.Prune {
		if err := m.pruneToManifest(&cfg, man); err != nil {
			return err
		}
	}

	return writeWorkspaceConfig(cfg)
}

// pruneToManifest removes skills and MCP servers from the workspace that are not
// named in the manifest, updating both the on-disk home and cfg.
func (m *Manager) pruneToManifest(cfg *WorkspaceConfig, man Manifest) error {
	keepSkill := make(map[string]bool, len(man.Skills))
	for _, s := range man.Skills {
		keepSkill[SkillName(s)] = true
	}
	var skills []SkillSource
	for _, s := range cfg.Skills {
		name := SkillName(s)
		if keepSkill[name] {
			skills = append(skills, s)
			continue
		}
		if err := os.RemoveAll(filepath.Join(cfg.WorkDir, "home", ".claude", "skills", name)); err != nil {
			return fmt.Errorf("remove skill dir: %v", err)
		}
	}
	cfg.Skills = skills

	for name := range cfg.MCPServers {
		if _, ok := man.MCPServers[name]; ok {
			continue
		}
		if err := removeMCPServer(cfg.WorkDir, name); err != nil {
			return err
		}
		delete(cfg.MCPServers, name)
	}
	if len(cfg.MCPServers) == 0 {
		cfg.MCPServers = nil
	}
	return nil
}

// existingMCPServers reads the MCP servers currently recorded in the workspace's
// home/.claude.json. A missing file yields a nil map.
func existingMCPServers(workDir string) (map[string]MCPServer, error) {
	path := filepath.Join(workDir, "home", ".claude.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read .claude.json: %v", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var root struct {
		MCPServers map[string]MCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse .claude.json: %v", err)
	}
	return root.MCPServers, nil
}

// mergeServerSecrets returns incoming with any blank Env/Headers values filled
// from existing, so a secret-stripped manifest does not wipe values already
// configured for the same server.
func mergeServerSecrets(existing, incoming MCPServer) MCPServer {
	incoming.Env = fillBlanks(incoming.Env, existing.Env)
	incoming.Headers = fillBlanks(incoming.Headers, existing.Headers)
	return incoming
}

// fillBlanks returns a copy of in where any empty value is replaced by a
// non-empty value for the same key in existing. in is returned unchanged when
// it has no entries.
func fillBlanks(in, existing map[string]string) map[string]string {
	if len(in) == 0 {
		return in
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if v == "" {
			if ev, ok := existing[k]; ok && ev != "" {
				v = ev
			}
		}
		out[k] = v
	}
	return out
}

// AddSkill seeds a skill into an existing workspace and records it in the
// workspace config so it is carried by export and duplicate. An existing skill
// installed under the same name is replaced.
func (m *Manager) AddSkill(ctx context.Context, workspaceName string, src SkillSource) error {
	cfg, err := m.Workspace(workspaceName)
	if err != nil {
		return err
	}
	if err := m.seedSkills(ctx, cfg.WorkDir, []SkillSource{src}); err != nil {
		return err
	}
	cfg.Skills = append(filterSkills(cfg.Skills, SkillName(src)), src)
	return writeWorkspaceConfig(cfg)
}

// RemoveSkill deletes a skill from an existing workspace by its installed name.
func (m *Manager) RemoveSkill(workspaceName, name string) error {
	cfg, err := m.Workspace(workspaceName)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(cfg.WorkDir, "home", ".claude", "skills", name)); err != nil {
		return fmt.Errorf("remove skill dir: %v", err)
	}
	cfg.Skills = filterSkills(cfg.Skills, name)
	return writeWorkspaceConfig(cfg)
}

// AddMCPServer merges an MCP server into an existing workspace and records it in
// the workspace config. An existing server with the same name is replaced.
func (m *Manager) AddMCPServer(workspaceName, name string, srv MCPServer) error {
	cfg, err := m.Workspace(workspaceName)
	if err != nil {
		return err
	}
	if err := mergeMCPServers(cfg.WorkDir, map[string]MCPServer{name: srv}); err != nil {
		return err
	}
	if cfg.MCPServers == nil {
		cfg.MCPServers = map[string]MCPServer{}
	}
	cfg.MCPServers[name] = srv
	return writeWorkspaceConfig(cfg)
}

// RemoveMCPServer removes an MCP server from an existing workspace by name.
func (m *Manager) RemoveMCPServer(workspaceName, name string) error {
	cfg, err := m.Workspace(workspaceName)
	if err != nil {
		return err
	}
	if err := removeMCPServer(cfg.WorkDir, name); err != nil {
		return err
	}
	delete(cfg.MCPServers, name)
	if len(cfg.MCPServers) == 0 {
		cfg.MCPServers = nil
	}
	return writeWorkspaceConfig(cfg)
}

// filterSkills returns skills excluding any whose installed name matches name.
func filterSkills(skills []SkillSource, name string) []SkillSource {
	var out []SkillSource
	for _, s := range skills {
		if SkillName(s) != name {
			out = append(out, s)
		}
	}
	return out
}

// removeMCPServer deletes a single server from the workspace's .claude.json,
// preserving all other content. A missing file is treated as success.
func removeMCPServer(workDir, name string) error {
	path := filepath.Join(workDir, "home", ".claude.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read .claude.json: %v", err)
	}
	root := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse .claude.json: %v", err)
		}
	}
	mcp, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return nil
	}
	delete(mcp, name)
	root["mcpServers"] = mcp

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode .claude.json: %v", err)
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
