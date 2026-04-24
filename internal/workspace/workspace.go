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
	Name    string    `yaml:"name"`
	Image   string    `yaml:"image"`
	Created time.Time `yaml:"created"`
	WorkDir string    `yaml:"-"` // derived from path at load time, not stored
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
