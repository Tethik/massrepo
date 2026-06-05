package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Label keys stored on session containers.
const (
	labelManaged        = "massrepo.managed"
	labelWorkspaceName  = "massrepo.workspace.name"
	labelSessionID      = "massrepo.session.id"
	labelSessionDir     = "massrepo.session.dir"
	labelSessionImage   = "massrepo.session.image"
	labelSessionCreated = "massrepo.session.created"
)

// WorkspaceConfig holds workspace metadata persisted to workspace.yaml.
type WorkspaceConfig struct {
	Name       string               `yaml:"name"`
	Image      string               `yaml:"image"`
	Created    time.Time            `yaml:"created"`
	Skills     []SkillSource        `yaml:"skills,omitempty"`
	MCPServers map[string]MCPServer `yaml:"mcp_servers,omitempty"`
	WorkDir    string               `yaml:"-"` // derived from path at load time, not stored
}

// SkillSource locates a Claude skill to seed into a workspace's home directory.
// It is either a local host directory (Path) or a git repository (Git plus Ref,
// with an optional Subdir within the repo). The two forms are mutually
// exclusive; only git-backed sources are portable across machines.
type SkillSource struct {
	Path   string `yaml:"path,omitempty" mapstructure:"path"`
	Git    string `yaml:"git,omitempty" mapstructure:"git"`
	Ref    string `yaml:"ref,omitempty" mapstructure:"ref"`
	Subdir string `yaml:"subdir,omitempty" mapstructure:"subdir"`
}

// Validate reports whether a skill source is well-formed. A source is either a
// local Path or a git repository; git sources must pin both a Ref and a Subdir
// (the directory within the repo that holds the skill), which also keeps the
// repo's .git tree out of the installed skill.
func (s SkillSource) Validate() error {
	switch {
	case s.Path != "" && s.Git != "":
		return fmt.Errorf("skill source must set either path or git, not both")
	case s.Path == "" && s.Git == "":
		return fmt.Errorf("skill source must set either path or git")
	case s.Git != "" && s.Ref == "":
		return fmt.Errorf("git skill %q requires a ref", s.Git)
	case s.Git != "" && s.Subdir == "":
		return fmt.Errorf("git skill %q requires a subdir", s.Git)
	}
	return nil
}

// UnmarshalYAML accepts either a scalar string (treated as a local Path) or a
// mapping with the git fields.
func (s *SkillSource) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return value.Decode(&s.Path)
	}
	// Alias the type to avoid recursing back into this method.
	type raw SkillSource
	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}
	*s = SkillSource(r)
	return nil
}

// MarshalYAML emits a bare scalar for local-path sources and a mapping for
// git-backed ones, mirroring UnmarshalYAML.
func (s SkillSource) MarshalYAML() (any, error) {
	if s.Git == "" {
		return s.Path, nil
	}
	type raw SkillSource
	return raw(s), nil
}

// MCPServer is a Model Context Protocol server definition. The same struct is
// read from config/manifest YAML and written verbatim into Claude's
// .claude.json (hence the json tags). For stdio servers set Command/Args/Env;
// for HTTP servers set URL/Headers and Type "http".
type MCPServer struct {
	Type    string            `yaml:"type,omitempty" mapstructure:"type" json:"type,omitempty"`
	Command string            `yaml:"command,omitempty" mapstructure:"command" json:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty" mapstructure:"args" json:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" mapstructure:"env" json:"env,omitempty"`
	URL     string            `yaml:"url,omitempty" mapstructure:"url" json:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" mapstructure:"headers" json:"headers,omitempty"`
}

// Session represents a running or stopped container belonging to a workspace.
// Each session holds its own copy of the workspace repos.
type Session struct {
	WorkspaceName string
	ID            string
	SessionDir    string // absolute host path to the session directory
	Image         string
	Created       time.Time
	Container     string // Docker container ID
	Status        string // Docker status string
}

// containerName returns the Docker container name for a session.
func containerName(workspaceName, sessionID string) string {
	return "massrepo-" + workspaceName + "-" + sessionID
}

// labelsForSession builds Docker labels for a session container.
func labelsForSession(workspaceName, sessionID, sessionDir, image string, created time.Time) map[string]string {
	return map[string]string{
		labelManaged:        "true",
		labelWorkspaceName:  workspaceName,
		labelSessionID:      sessionID,
		labelSessionDir:     sessionDir,
		labelSessionImage:   image,
		labelSessionCreated: created.UTC().Format(time.RFC3339),
	}
}

// sessionFromLabels parses Docker container labels into a Session.
func sessionFromLabels(containerID, status string, labels map[string]string) (Session, error) {
	required := []string{labelWorkspaceName, labelSessionID, labelSessionDir, labelSessionImage, labelSessionCreated}
	for _, key := range required {
		if _, ok := labels[key]; !ok {
			return Session{}, fmt.Errorf("container %s missing label %q", containerID, key)
		}
	}
	created, err := time.Parse(time.RFC3339, labels[labelSessionCreated])
	if err != nil {
		return Session{}, fmt.Errorf("container %s: invalid created label: %w", containerID, err)
	}
	return Session{
		WorkspaceName: labels[labelWorkspaceName],
		ID:            labels[labelSessionID],
		SessionDir:    labels[labelSessionDir],
		Image:         labels[labelSessionImage],
		Created:       created,
		Container:     containerID,
		Status:        status,
	}, nil
}

// workspaceConfigPath returns the path to the workspace metadata file.
func workspaceConfigPath(workDir string) string {
	return filepath.Join(workDir, "workspace.yaml")
}

// writeWorkspaceConfig persists a WorkspaceConfig to workspace.yaml in its WorkDir.
func writeWorkspaceConfig(cfg WorkspaceConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(workspaceConfigPath(cfg.WorkDir), data, 0o644)
}

// readWorkspaceConfig loads a WorkspaceConfig from the given workspace directory.
func readWorkspaceConfig(workDir string) (WorkspaceConfig, error) {
	data, err := os.ReadFile(workspaceConfigPath(workDir))
	if err != nil {
		return WorkspaceConfig{}, fmt.Errorf("read workspace config: %v", err)
	}
	var cfg WorkspaceConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return WorkspaceConfig{}, fmt.Errorf("parse workspace config: %v", err)
	}
	cfg.WorkDir = workDir
	return cfg, nil
}
