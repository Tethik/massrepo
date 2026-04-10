package workspace

import (
	"fmt"
	"strings"
	"time"
)

const (
	labelManaged = "massrepo.managed"
	labelName    = "massrepo.workspace.name"
	labelRepos   = "massrepo.workspace.repos"    // comma-separated list of repo paths
	labelWorkDir = "massrepo.workspace.workdir"  // abs path to workspace root on host
	labelImage   = "massrepo.workspace.image"
	labelCreated = "massrepo.workspace.created"
)

// Workspace is a parsed view of a running or stopped managed container.
type Workspace struct {
	Name      string
	Repos     []string  // relative repo paths under repositories/, e.g. ["einride/security-infra"]
	WorkDir   string    // absolute path to the workspace root on the host (~/.massrepo/workspaces/<name>)
	Image     string    // Docker image used to create the container
	Created   time.Time
	Container string    // Docker container ID
	Status    string    // Docker status string, e.g. "running", "exited"
}

// containerName returns the Docker container name for a given workspace name.
func containerName(name string) string {
	return "massrepo-" + name
}

// labelsForWorkspace builds the Docker label map for a new container.
func labelsForWorkspace(w Workspace) map[string]string {
	return map[string]string{
		labelManaged: "true",
		labelName:    w.Name,
		labelRepos:   strings.Join(w.Repos, ","),
		labelWorkDir: w.WorkDir,
		labelImage:   w.Image,
		labelCreated: w.Created.UTC().Format(time.RFC3339),
	}
}

// workspaceFromLabels parses Docker container labels into a Workspace.
// Returns an error if any required label is missing.
func workspaceFromLabels(containerID, status string, labels map[string]string) (Workspace, error) {
	required := []string{labelName, labelRepos, labelWorkDir, labelImage, labelCreated}
	for _, key := range required {
		if _, ok := labels[key]; !ok {
			return Workspace{}, fmt.Errorf("container %s missing label %q", containerID, key)
		}
	}

	created, err := time.Parse(time.RFC3339, labels[labelCreated])
	if err != nil {
		return Workspace{}, fmt.Errorf("container %s: invalid created label: %w", containerID, err)
	}

	repos := strings.Split(labels[labelRepos], ",")

	return Workspace{
		Name:      labels[labelName],
		Repos:     repos,
		WorkDir:   labels[labelWorkDir],
		Image:     labels[labelImage],
		Created:   created,
		Container: containerID,
		Status:    status,
	}, nil
}
