package workspace

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
)

//go:embed session_readme.md
var sessionReadme []byte

//go:embed session_claude.md
var sessionClaudeTemplate string

// containerHome is the home directory of the container user defined in the image.
const containerHome = "/home/node"

// containerWorkspace is the top-level directory inside the container where
// session repos are bind-mounted. It deliberately lives outside containerHome
// so the home directory can itself be a single bind mount without colliding
// with per-repo mount targets.
const containerWorkspace = "/workspace"

// Manager orchestrates workspace and session lifecycle against a Docker daemon.
type Manager struct {
	docker        *client.Client
	reposDir      string // absolute path to repositories root
	workspacesDir string // absolute path to the workspace parent directory
	imagesRoot    string // absolute path to the images root holding <image>/Dockerfile
	skillCacheDir string // absolute path to the cache of cloned git skills
	defaultImage  string
}

// NewManager constructs a Manager. It connects to the Docker daemon using
// environment variables (DOCKER_HOST, etc.) with API version negotiation.
func NewManager(reposDir, workspacesDir, imagesRoot, defaultImage string) (*Manager, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to docker: %v", err)
	}
	if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
		return nil, fmt.Errorf("create workspaces dir: %v", err)
	}
	return &Manager{
		docker:        cli,
		reposDir:      reposDir,
		workspacesDir: workspacesDir,
		imagesRoot:    imagesRoot,
		// The skill cache lives alongside the workspace dir under data_path.
		skillCacheDir: filepath.Join(filepath.Dir(workspacesDir), "skillcache"),
		defaultImage:  defaultImage,
	}, nil
}

// CreateOptions holds parameters for workspace creation.
type CreateOptions struct {
	Name       string       // workspace name; must be unique
	Image      string       // Docker image; falls back to defaultImage if empty
	Dockerfile string       // optional inline Dockerfile content for Image (from a manifest)
	Pull       bool         // allow fetching Image from a registry when not buildable
	Assets     ClaudeAssets // skills and MCP servers to seed into the workspace home
}

// Create sets up the workspace directory structure and persists its configuration.
// No container is started; use Shell to create sessions.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (WorkspaceConfig, error) {
	workDir := filepath.Join(m.workspacesDir, opts.Name)
	if _, err := os.Stat(workDir); err == nil {
		return WorkspaceConfig{}, fmt.Errorf("workspace %q already exists", opts.Name)
	}

	image := opts.Image
	if image == "" {
		image = m.defaultImage
	}

	// A manifest-provided Dockerfile is installed before resolving the image so
	// it builds from the shared recipe rather than a registry.
	if opts.Dockerfile != "" {
		if err := m.installDockerfile(image, opts.Dockerfile); err != nil {
			return WorkspaceConfig{}, err
		}
	}
	if err := m.ensureImage(ctx, image, opts.Pull); err != nil {
		return WorkspaceConfig{}, err
	}

	if err := createWorkspaceDirs(workDir); err != nil {
		return WorkspaceConfig{}, err
	}

	if err := m.seedClaudeAssets(ctx, workDir, opts.Assets); err != nil {
		_ = os.RemoveAll(workDir)
		return WorkspaceConfig{}, fmt.Errorf("seed claude assets: %v", err)
	}

	cfg := WorkspaceConfig{
		Name:       opts.Name,
		Image:      image,
		Created:    time.Now().UTC(),
		Skills:     opts.Assets.Skills,
		MCPServers: opts.Assets.MCPServers,
		WorkDir:    workDir,
	}
	if err := writeWorkspaceConfig(cfg); err != nil {
		_ = os.RemoveAll(workDir)
		return WorkspaceConfig{}, fmt.Errorf("write workspace config: %v", err)
	}
	return cfg, nil
}

// Workspace returns the configuration for a workspace by name.
func (m *Manager) Workspace(workspaceName string) (WorkspaceConfig, error) {
	workDir := filepath.Join(m.workspacesDir, workspaceName)
	return readWorkspaceConfig(workDir)
}

// Shell creates a new session for the workspace and opens an interactive shell inside it.
// repos lists the relative repo paths to copy into the session.
// Returns the session ID so the caller can decide what to do after the shell exits.
func (m *Manager) Shell(ctx context.Context, workspaceName string, repos []string, shell string) (string, error) {
	if len(repos) == 0 {
		return "", fmt.Errorf("at least one repo is required")
	}
	cfg, err := m.Workspace(workspaceName)
	if err != nil {
		return "", err
	}
	// Rebuild the image if its Dockerfile changed since the workspace was created.
	if err := m.ensureImage(ctx, cfg.Image, false); err != nil {
		return "", err
	}
	if err := m.ensureRepos(ctx, repos); err != nil {
		return "", err
	}
	fmt.Printf("Creating session in workspace %q with %d repo(s)...\n", workspaceName, len(repos))
	s, err := m.newSession(ctx, cfg, repos)
	if err != nil {
		return "", err
	}
	fmt.Printf("Session %s ready. Opening shell...\n", s.ID)
	if isTerminal(os.Stdout) && !welcomeShown(cfg.WorkDir) {
		fmt.Print(welcomeBanner(cfg))
		_ = markWelcomeShown(cfg.WorkDir) // best-effort; don't block the shell
	}
	restoreTitle := setTerminalTitle(workspaceName + "/" + s.ID)
	defer restoreTitle()
	cmd := exec.CommandContext(ctx, "docker", "exec", "-it",
		containerName(workspaceName, s.ID), shell)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return s.ID, cmd.Run()
}

// setTerminalTitle pushes the current title onto the xterm title stack and
// sets a new one. The returned function pops the stack to restore the
// previous title. No-op if stdout is not a terminal.
func setTerminalTitle(title string) func() {
	if !isTerminal(os.Stdout) {
		return func() {}
	}
	// CSI 22;0t — push window+icon title; OSC 0 — set both.
	fmt.Fprintf(os.Stdout, "\033[22;0t\033]0;%s\007", title)
	return func() {
		// CSI 23;0t — pop the stacked title.
		fmt.Fprint(os.Stdout, "\033[23;0t")
	}
}

// renderSessionClaude fills in the {{REPOS}} placeholder in the embedded
// CLAUDE.md template with a bullet list of repo paths inside the container.
func renderSessionClaude(repos []string) string {
	var list strings.Builder
	if len(repos) == 0 {
		list.WriteString("\n_None — this session was started without any repos._\n")
	} else {
		list.WriteString("\n")
		for _, r := range repos {
			fmt.Fprintf(&list, "- `%s/%s`\n", containerWorkspace, r)
		}
	}
	return strings.Replace(sessionClaudeTemplate, "{{REPOS}}", list.String(), 1)
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// newSession creates a session for the given workspace: copies repos, starts a container.
func (m *Manager) newSession(ctx context.Context, cfg WorkspaceConfig, repos []string) (Session, error) {
	sessionID := uuid.NewString()
	sessionDir := filepath.Join(cfg.WorkDir, "sessions", sessionID)
	workspaceDir := filepath.Join(sessionDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return Session{}, fmt.Errorf("create session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "README.md"), sessionReadme, 0o644); err != nil {
		return Session{}, fmt.Errorf("write session README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "CLAUDE.md"), []byte(renderSessionClaude(repos)), 0o644); err != nil {
		return Session{}, fmt.Errorf("write session CLAUDE.md: %v", err)
	}
	// Ensure all data dirs exist before the container starts — Docker creates
	// missing bind-mount sources as root, which makes them unwritable in the container.
	if err := createWorkspaceDirs(cfg.WorkDir); err != nil {
		return Session{}, fmt.Errorf("ensure workspace dirs: %v", err)
	}

	for _, repo := range repos {
		fmt.Printf("Copying %s...\n", repo)
		src := filepath.Join(m.reposDir, filepath.FromSlash(repo))
		dst := filepath.Join(workspaceDir, filepath.FromSlash(repo))
		if err := copyDir(src, dst); err != nil {
			_ = os.RemoveAll(sessionDir)
			return Session{}, fmt.Errorf("copy repo %q: %v", repo, err)
		}
	}

	now := time.Now()
	sshBinds, sshEnv := sshAgentConfig()
	resp, err := m.docker.ContainerCreate(ctx,
		&container.Config{
			Image:      cfg.Image,
			Entrypoint: []string{"sleep", "infinity"},
			User:       hostUser(),
			Env:        append(sshEnv, "HOME="+containerHome),
			Labels:     labelsForSession(cfg.Name, sessionID, sessionDir, cfg.Image, now),
		},
		&container.HostConfig{
			Binds: append(
				[]string{
					workspaceDir + ":" + containerWorkspace,
					homeBind(cfg.WorkDir),
				},
				sshBinds...,
			),
		},
		&network.NetworkingConfig{},
		nil,
		containerName(cfg.Name, sessionID),
	)
	if err != nil {
		_ = os.RemoveAll(sessionDir)
		return Session{}, fmt.Errorf("create container: %v", err)
	}

	if err := m.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = m.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		_ = os.RemoveAll(sessionDir)
		return Session{}, fmt.Errorf("start container: %v", err)
	}

	return Session{
		WorkspaceName: cfg.Name,
		ID:            sessionID,
		SessionDir:    sessionDir,
		Image:         cfg.Image,
		Created:       now,
		Container:     resp.ID,
		Status:        "running",
	}, nil
}

// ListSessions returns all managed sessions visible to the Docker daemon.
// If workspaceName is non-empty, results are filtered to that workspace.
func (m *Manager) ListSessions(ctx context.Context, workspaceName string) ([]Session, error) {
	args := filters.NewArgs(filters.Arg("label", labelManaged+"=true"))
	if workspaceName != "" {
		args.Add("label", labelWorkspaceName+"="+workspaceName)
	}
	containers, err := m.docker.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, fmt.Errorf("list containers: %v", err)
	}
	sessions := make([]Session, 0, len(containers))
	for _, c := range containers {
		s, err := sessionFromLabels(c.ID, c.Status, c.Labels)
		if err != nil {
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// Session returns a single session by workspace name and session ID.
func (m *Manager) Session(ctx context.Context, workspaceName, sessionID string) (Session, error) {
	containers, err := m.docker.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", labelManaged+"=true"),
			filters.Arg("name", containerName(workspaceName, sessionID)),
		),
	})
	if err != nil {
		return Session{}, fmt.Errorf("get session: %v", err)
	}
	for _, c := range containers {
		s, err := sessionFromLabels(c.ID, c.Status, c.Labels)
		if err != nil {
			continue
		}
		if s.WorkspaceName == workspaceName && s.ID == sessionID {
			return s, nil
		}
	}
	return Session{}, fmt.Errorf("session %q/%q not found", workspaceName, sessionID)
}

// StopSession stops a session's container.
func (m *Manager) StopSession(ctx context.Context, workspaceName, sessionID string) error {
	s, err := m.Session(ctx, workspaceName, sessionID)
	if err != nil {
		return err
	}
	if err := m.docker.ContainerStop(ctx, s.Container, container.StopOptions{}); err != nil {
		return fmt.Errorf("stop container: %v", err)
	}
	return nil
}

// RemoveSession stops and removes a session's container and its directory.
func (m *Manager) RemoveSession(ctx context.Context, workspaceName, sessionID string) error {
	s, err := m.Session(ctx, workspaceName, sessionID)
	if err != nil {
		return err
	}
	return m.removeSession(ctx, s)
}

// removeSession is the internal implementation for removing a single session.
func (m *Manager) removeSession(ctx context.Context, s Session) error {
	_ = m.docker.ContainerStop(ctx, s.Container, container.StopOptions{})
	var containerErr error
	if err := m.docker.ContainerRemove(ctx, s.Container, container.RemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
		containerErr = fmt.Errorf("remove container: %v", err)
	}
	if err := os.RemoveAll(s.SessionDir); err != nil {
		return errors.Join(containerErr, fmt.Errorf("remove session dir: %v", err))
	}
	return containerErr
}

// Remove stops and removes all sessions for the workspace, then deletes the workspace directory.
func (m *Manager) Remove(ctx context.Context, workspaceName string) error {
	sessions, err := m.ListSessions(ctx, workspaceName)
	if err != nil {
		return err
	}
	for _, s := range sessions {
		if err := m.removeSession(ctx, s); err != nil {
			return err
		}
	}
	workDir := filepath.Join(m.workspacesDir, workspaceName)
	if err := os.RemoveAll(workDir); err != nil {
		return fmt.Errorf("remove workspace dir: %v", err)
	}
	return nil
}

// Duplicate creates a new workspace with the same image, skills and MCP servers
// as the source. No sessions are copied; the new workspace starts empty and its
// assets are re-seeded from the source's recorded configuration.
func (m *Manager) Duplicate(ctx context.Context, sourceName, destName string) (WorkspaceConfig, error) {
	src, err := m.Workspace(sourceName)
	if err != nil {
		return WorkspaceConfig{}, fmt.Errorf("source: %v", err)
	}
	return m.Create(ctx, CreateOptions{
		Name:  destName,
		Image: src.Image,
		Assets: ClaudeAssets{
			Skills:     src.Skills,
			MCPServers: src.MCPServers,
		},
	})
}

// SetImage changes the Docker image recorded for an existing workspace, ensuring
// the new image is available first (built from its Dockerfile, or pulled when
// pull is set). Existing sessions keep their current image; new sessions created
// from this workspace use the new one.
func (m *Manager) SetImage(ctx context.Context, workspaceName, image string, pull bool) (WorkspaceConfig, error) {
	if image == "" {
		return WorkspaceConfig{}, fmt.Errorf("image is required")
	}
	cfg, err := m.Workspace(workspaceName)
	if err != nil {
		return WorkspaceConfig{}, err
	}
	if err := m.ensureImage(ctx, image, pull); err != nil {
		return WorkspaceConfig{}, err
	}
	cfg.Image = image
	if err := writeWorkspaceConfig(cfg); err != nil {
		return WorkspaceConfig{}, fmt.Errorf("write workspace config: %v", err)
	}
	return cfg, nil
}

// homeBind returns a single bind mount for the workspace's persistent home
// directory. Mounting the whole home dir (rather than individual files like
// .claude.json) avoids inode-pinning issues with atomic file rewrites.
func homeBind(workDir string) string {
	return filepath.Join(workDir, "home") + ":" + containerHome
}

// hostUser returns a "uid:gid" string for the current process so containers
// run as the same identity as the host user.
func hostUser() string {
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

// sshAgentConfig returns bind mounts and environment variables needed to
// forward the host SSH agent into a container. If SSH_AUTH_SOCK is not set,
// both slices are empty and SSH forwarding is silently skipped.
func sshAgentConfig() (binds, env []string) {
	sock, ok := os.LookupEnv("SSH_AUTH_SOCK")
	if !ok || sock == "" {
		return nil, nil
	}
	return []string{sock + ":/run/ssh-agent.sock"},
		[]string{"SSH_AUTH_SOCK=/run/ssh-agent.sock"}
}

// labelContextSHA records the hash of the build context an image was built from,
// so massrepo can detect when the Dockerfile/context has changed and rebuild.
const labelContextSHA = "massrepo.context-sha"

// BuildImage builds the Docker image for imageName unconditionally, using the
// image's folder under the images root as the build context. The build context
// hash is stamped onto the image as a label for change detection.
func (m *Manager) BuildImage(ctx context.Context, imageName string) error {
	dir, err := m.resolveImageDir(imageName)
	if err != nil {
		return err
	}
	if dir == "" {
		return fmt.Errorf("no Dockerfile for image %q under %s", imageName, m.imagesRoot)
	}
	sha, err := hashImageContext(dir)
	if err != nil {
		return err
	}
	dockerfile := filepath.Join(dir, "Dockerfile")
	fmt.Printf("Building image %q from %s...\n", imageName, dockerfile)
	cmd := exec.CommandContext(ctx, "docker", "build",
		"-t", imageName,
		"-f", dockerfile,
		"--label", labelContextSHA+"="+sha,
		dir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build image %q: %v", imageName, err)
	}
	return nil
}

// hashImageContext returns a deterministic hash over every file in the build
// context directory (relative path + contents), used to detect changes.
func hashImageContext(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash image context: %v", err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(h, "%s\x00", filepath.ToSlash(rel))
		data, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("hash image context: %v", err)
		}
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ensureImage makes imageName available locally without ever silently pulling
// from a registry. It builds from a resolvable Dockerfile (on disk or embedded)
// when the image is missing or when its build context has changed since it was
// last built (detected via the labelContextSHA label). An up-to-date image is
// used as-is. If the image is missing and not buildable, pull opts into a
// registry fetch; otherwise a clear error is returned.
func (m *Manager) ensureImage(ctx context.Context, imageName string, pull bool) error {
	inspect, err := m.docker.ImageInspect(ctx, imageName)
	present := err == nil
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspect image: %v", err)
	}

	dir, err := m.resolveImageDir(imageName)
	if err != nil {
		return err
	}

	if dir == "" {
		// No Dockerfile to build from.
		if present {
			return nil
		}
		if pull {
			return m.pullImage(ctx, imageName)
		}
		return fmt.Errorf("image %q is not present and has no Dockerfile under %s; pass --pull to fetch it from a registry",
			imageName, m.imagesRoot)
	}

	if present {
		// Rebuild only when the build context changed since the last build.
		sha, err := hashImageContext(dir)
		if err != nil {
			return err
		}
		if inspect.Config != nil && inspect.Config.Labels[labelContextSHA] == sha {
			return nil
		}
		fmt.Printf("Dockerfile for %q changed; rebuilding...\n", imageName)
	}
	return m.BuildImage(ctx, imageName)
}

// pullImage fetches imageName from a registry via the docker CLI.
func (m *Manager) pullImage(ctx context.Context, imageName string) error {
	fmt.Printf("Pulling image %q...\n", imageName)
	cmd := exec.CommandContext(ctx, "docker", "pull", imageName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pull image %q: %v", imageName, err)
	}
	return nil
}

// imageDirFor returns the build-context directory for imageName (its Dockerfile
// lives at <dir>/Dockerfile). The tag is ignored.
func (m *Manager) imageDirFor(imageName string) string {
	return filepath.Join(m.imagesRoot, filepath.FromSlash(stripTag(imageName)))
}

// resolveImageDir returns the build-context directory for imageName, or "" if no
// Dockerfile is available. An embedded default is materialized into the images
// root on demand (without clobbering an existing on-disk Dockerfile).
func (m *Manager) resolveImageDir(imageName string) (string, error) {
	dir := m.imageDirFor(imageName)
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err == nil {
		return dir, nil
	}
	ok, err := materializeEmbeddedImage(imageName, dir)
	if err != nil {
		return "", err
	}
	if ok {
		return dir, nil
	}
	return "", nil
}

// installDockerfile writes content as the Dockerfile for imageName under the
// images root (used when importing a manifest that carries an inline Dockerfile).
func (m *Manager) installDockerfile(imageName, content string) error {
	dir := m.imageDirFor(imageName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create image dir: %v", err)
	}
	return os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(content), 0o644)
}

// createWorkspaceDirs creates the standard directory layout for a new workspace.
func createWorkspaceDirs(workDir string) error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(workDir, "sessions"), 0o755},
		{filepath.Join(workDir, "home"), 0o755},
		{filepath.Join(workDir, "home", ".ssh"), 0o700},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return fmt.Errorf("create workspace dir %s: %v", d.path, err)
		}
	}
	return nil
}

// copyDir recursively copies the contents of src into dst. Symlinks are
// recreated as symlinks (their target string is preserved verbatim) rather
// than dereferenced, so links pointing within the tree keep working and
// links to directories don't get walked into.
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
		if d.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			// Remove any existing entry at target so re-copying is idempotent.
			_ = os.Remove(target)
			return os.Symlink(linkTarget, target)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

// copyFile copies a single file from src to dst, preserving permissions.
// The file is created with owner-write to avoid failures on read-only sources
// (e.g. git pack files at 0444); permissions are restored after writing.
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

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode()|0o200)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode())
}
