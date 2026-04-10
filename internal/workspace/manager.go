package workspace

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// Manager orchestrates workspace lifecycle against a Docker daemon.
type Manager struct {
	docker        *client.Client
	reposDir      string // absolute path to repositories/ root
	workspacesDir string // absolute path to ~/.massrepo/workspaces/
	imagesDir     string // absolute path to the images/ directory containing Dockerfiles
	defaultImage  string
}

// NewManager constructs a Manager. It connects to the Docker daemon using
// environment variables (DOCKER_HOST, etc.) with API version negotiation.
func NewManager(reposDir, workspacesDir, imagesDir, defaultImage string) (*Manager, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to docker: %w", err)
	}
	if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
		return nil, fmt.Errorf("create workspaces dir: %w", err)
	}
	return &Manager{
		docker:        cli,
		reposDir:      reposDir,
		workspacesDir: workspacesDir,
		imagesDir:     imagesDir,
		defaultImage:  defaultImage,
	}, nil
}

// CreateOptions holds parameters for workspace creation.
type CreateOptions struct {
	Name  string   // workspace name; must be unique
	Repos []string // relative repo paths, e.g. ["einride/security-infra"]
	Image string   // Docker image; falls back to defaultImage if empty
}

// Create copies each repo into a per-workspace directory, then creates and
// starts a Docker container with each repo bind-mounted at /workspace/<org>/<repo>.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (Workspace, error) {
	if len(opts.Repos) == 0 {
		return Workspace{}, fmt.Errorf("at least one repo is required")
	}

	if _, err := m.Get(ctx, opts.Name); err == nil {
		return Workspace{}, fmt.Errorf("workspace %q already exists", opts.Name)
	}

	for _, repo := range opts.Repos {
		src := filepath.Join(m.reposDir, filepath.FromSlash(repo))
		if _, err := os.Stat(src); os.IsNotExist(err) {
			return Workspace{}, fmt.Errorf("repo %q not found in %s", repo, m.reposDir)
		}
	}

	image := opts.Image
	if image == "" {
		image = m.defaultImage
	}

	if err := m.ensureImage(ctx, image); err != nil {
		return Workspace{}, err
	}

	workDir := filepath.Join(m.workspacesDir, opts.Name)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return Workspace{}, fmt.Errorf("create workspace dir: %w", err)
	}

	for _, repo := range opts.Repos {
		src := filepath.Join(m.reposDir, filepath.FromSlash(repo))
		dst := filepath.Join(workDir, filepath.FromSlash(repo))
		if err := copyDir(src, dst); err != nil {
			_ = os.RemoveAll(workDir)
			return Workspace{}, fmt.Errorf("copy repo %q: %w", repo, err)
		}
	}

	now := time.Now()
	w := Workspace{
		Name:    opts.Name,
		Repos:   opts.Repos,
		WorkDir: workDir,
		Image:   image,
		Created: now,
	}

	sshBinds, sshEnv := sshAgentConfig()
	resp, err := m.docker.ContainerCreate(ctx,
		&container.Config{
			Image:      image,
			Entrypoint: []string{"sleep", "infinity"},
			User:       hostUser(),
			Env:        sshEnv,
			Labels:     labelsForWorkspace(w),
		},
		&container.HostConfig{
			Binds: append(repoBinds(workDir, opts.Repos), sshBinds...),
		},
		&network.NetworkingConfig{},
		nil,
		containerName(opts.Name),
	)
	if err != nil {
		_ = os.RemoveAll(workDir)
		return Workspace{}, fmt.Errorf("create container: %w", err)
	}

	if err := m.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = m.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		_ = os.RemoveAll(workDir)
		return Workspace{}, fmt.Errorf("start container: %w", err)
	}

	w.Container = resp.ID
	w.Status = "running"
	return w, nil
}

// repoBinds builds the Docker bind-mount strings for each repo.
// Each repo is mounted at /workspace/<org>/<repo> inside the container.
func repoBinds(workDir string, repos []string) []string {
	binds := make([]string, len(repos))
	for i, repo := range repos {
		hostPath := filepath.Join(workDir, filepath.FromSlash(repo))
		containerPath := "/workspace/" + repo
		binds[i] = hostPath + ":" + containerPath
	}
	return binds
}

// hostUser returns a "uid:gid" string for the current process so containers
// run as the same identity as the host user. This ensures bind-mounted
// workspace files (owned by the host user) are writable inside the container.
func hostUser() string {
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

// sshAgentConfig returns additional bind mounts and environment variables needed
// to forward the host SSH agent into a container. If SSH_AUTH_SOCK is not set,
// both slices are empty and SSH forwarding is silently skipped.
// The socket is exposed at /run/ssh-agent.sock inside the container so the path
// is stable across workspaces regardless of the host socket location.
func sshAgentConfig() (binds, env []string) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil
	}
	return []string{sock + ":/run/ssh-agent.sock"},
		[]string{"SSH_AUTH_SOCK=/run/ssh-agent.sock"}
}

// List returns all massrepo-managed workspaces visible to the Docker daemon.
func (m *Manager) List(ctx context.Context) ([]Workspace, error) {
	containers, err := m.docker.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelManaged+"=true")),
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	workspaces := make([]Workspace, 0, len(containers))
	for _, c := range containers {
		w, err := workspaceFromLabels(c.ID, c.Status, c.Labels)
		if err != nil {
			continue // skip containers with malformed labels
		}
		workspaces = append(workspaces, w)
	}
	return workspaces, nil
}

// Get returns a single workspace by name.
func (m *Manager) Get(ctx context.Context, name string) (Workspace, error) {
	containers, err := m.docker.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", labelManaged+"=true"),
			filters.Arg("name", containerName(name)),
		),
	})
	if err != nil {
		return Workspace{}, fmt.Errorf("get workspace: %w", err)
	}
	for _, c := range containers {
		w, err := workspaceFromLabels(c.ID, c.Status, c.Labels)
		if err != nil {
			continue
		}
		if w.Name == name {
			return w, nil
		}
	}
	return Workspace{}, fmt.Errorf("workspace %q not found", name)
}

// Shell opens an interactive shell in the workspace container by shelling out
// to `docker exec`. The container is started first if it is not running.
func (m *Manager) Shell(ctx context.Context, name string, shell string) error {
	w, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if err := m.ensureRunning(ctx, w.Container); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-it", containerName(name), shell)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Stop stops the workspace container.
func (m *Manager) Stop(ctx context.Context, name string) error {
	w, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if err := m.docker.ContainerStop(ctx, w.Container, container.StopOptions{}); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

// Start starts a stopped workspace container.
func (m *Manager) Start(ctx context.Context, name string) error {
	w, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	return m.ensureRunning(ctx, w.Container)
}

// Remove stops the container if running, removes it, then deletes the workspace
// directory from the host.
func (m *Manager) Remove(ctx context.Context, name string) error {
	w, err := m.Get(ctx, name)
	if err != nil {
		return err
	}

	_ = m.docker.ContainerStop(ctx, w.Container, container.StopOptions{})

	if err := m.docker.ContainerRemove(ctx, w.Container, container.RemoveOptions{}); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}

	if err := os.RemoveAll(w.WorkDir); err != nil {
		return fmt.Errorf("remove workspace dir: %w", err)
	}
	return nil
}

// Duplicate snapshots the source workspace into a new workspace named dest.
// It commits the source container filesystem and copies the workspace directory
// so both workspaces are fully independent.
func (m *Manager) Duplicate(ctx context.Context, sourceName, destName string) (Workspace, error) {
	src, err := m.Get(ctx, sourceName)
	if err != nil {
		return Workspace{}, fmt.Errorf("source: %w", err)
	}

	if _, err := m.Get(ctx, destName); err == nil {
		return Workspace{}, fmt.Errorf("workspace %q already exists", destName)
	}

	snapshotTag := fmt.Sprintf("massrepo-snapshot-%s:%d", destName, time.Now().Unix())

	commitResp, err := m.docker.ContainerCommit(ctx, src.Container, container.CommitOptions{
		Reference: snapshotTag,
	})
	if err != nil {
		return Workspace{}, fmt.Errorf("commit container: %w", err)
	}
	_ = commitResp // ID available if needed for cleanup

	destWorkDir := filepath.Join(m.workspacesDir, destName)
	if err := os.MkdirAll(destWorkDir, 0o755); err != nil {
		return Workspace{}, fmt.Errorf("create dest dir: %w", err)
	}

	if err := copyDir(src.WorkDir, destWorkDir); err != nil {
		_ = os.RemoveAll(destWorkDir)
		return Workspace{}, fmt.Errorf("copy workspace dir: %w", err)
	}

	now := time.Now()
	dest := Workspace{
		Name:    destName,
		Repos:   src.Repos,
		WorkDir: destWorkDir,
		Image:   snapshotTag,
		Created: now,
	}

	sshBinds, sshEnv := sshAgentConfig()
	resp, err := m.docker.ContainerCreate(ctx,
		&container.Config{
			Image:      snapshotTag,
			Entrypoint: []string{"sleep", "infinity"},
			User:       hostUser(),
			Env:        sshEnv,
			Labels:     labelsForWorkspace(dest),
		},
		&container.HostConfig{
			Binds: append(repoBinds(destWorkDir, src.Repos), sshBinds...),
		},
		&network.NetworkingConfig{},
		nil,
		containerName(destName),
	)
	if err != nil {
		_ = os.RemoveAll(destWorkDir)
		return Workspace{}, fmt.Errorf("create container: %w", err)
	}

	if err := m.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = m.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		_ = os.RemoveAll(destWorkDir)
		return Workspace{}, fmt.Errorf("start container: %w", err)
	}

	dest.Container = resp.ID
	dest.Status = "running"
	return dest, nil
}

// BuildImage builds the Docker image for imageName unconditionally.
// The Dockerfile is resolved from the image name convention:
// "massrepo-claude:latest" → "<imagesDir>/Dockerfile.claude".
// Returns an error if the name does not follow the convention or the
// Dockerfile does not exist.
func (m *Manager) BuildImage(ctx context.Context, imageName string) error {
	dockerfile := m.dockerfileFor(imageName)
	if dockerfile == "" {
		return fmt.Errorf("image %q does not follow the massrepo-<name> naming convention", imageName)
	}
	if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
		return fmt.Errorf("no Dockerfile for %q at %s", imageName, dockerfile)
	}

	fmt.Printf("Building image %q from %s...\n", imageName, dockerfile)
	cmd := exec.CommandContext(ctx, "docker", "build",
		"-t", imageName,
		"-f", dockerfile,
		m.imagesDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build image %q: %w", imageName, err)
	}
	return nil
}

// ensureImage builds imageName if it is not already present locally.
// Non-massrepo image names are skipped; Docker will surface the error at
// container creation time if the image is truly missing.
func (m *Manager) ensureImage(ctx context.Context, imageName string) error {
	_, err := m.docker.ImageInspect(ctx, imageName)
	if err == nil {
		return nil // image already present
	}
	if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspect image: %w", err)
	}
	if m.dockerfileFor(imageName) == "" {
		return nil // non-massrepo image, skip
	}
	return m.BuildImage(ctx, imageName)
}

// dockerfileFor maps an image name to its Dockerfile path inside imagesDir.
// "massrepo-claude:latest" → "<imagesDir>/Dockerfile.claude"
// Returns "" if the name does not match the expected convention.
func (m *Manager) dockerfileFor(imageName string) string {
	// Strip tag.
	name := strings.SplitN(imageName, ":", 2)[0]
	// Must start with "massrepo-".
	const prefix = "massrepo-"
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	suffix := strings.TrimPrefix(name, prefix)
	if suffix == "" {
		return ""
	}
	return filepath.Join(m.imagesDir, "Dockerfile."+suffix)
}

// ensureRunning starts the container if it is not already running.
func (m *Manager) ensureRunning(ctx context.Context, containerID string) error {
	info, err := m.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("inspect container: %w", err)
	}
	if info.State != nil && info.State.Running {
		return nil
	}
	if err := m.docker.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

// copyDir recursively copies the contents of src into dst.
// dst must already exist or be creatable.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		return copyFile(path, target)
	})
}

// copyFile copies a single file from src to dst, preserving permissions.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
