# massrepo

Run security analysis, patching, and LLM tasks across many repositories at scale using sandboxed Docker workspaces.

## Concepts

**Workspace** — a named environment that persists authentication state (Claude config, git credentials, SSH keys) across sessions. Create one per task context.

**Session** — a short-lived Docker container spun up from a workspace. Each session gets its own copy of the requested repos so multiple sessions can work on the same repo independently.

**Repo cache** — repositories are cloned on demand from GitHub into `~/repos` (configurable) and reused across sessions. Think of it as a local mirror, not a working copy.

**Groups** — logical collections of repositories referenced by `kind:name`. Groups can be defined statically in config (`team:<name>`, `system:<name>`), resolved dynamically from a Backstage catalog, or expanded from GitHub via the `gh` CLI (`org:<name>`, `user:<name>`).

## Prerequisites

- Docker
- Go 1.22+ (to build from source)
- [`gh`](https://cli.github.com/) — GitHub CLI, authenticated (`gh auth login`)
- SSH key with GitHub access (for cloning)

## Installing

```sh
go install github.com/Tethik/massrepo/cmd/massrepo@latest
```

Installs the latest tagged release into `$GOBIN` (defaults to `~/go/bin` —
make sure it's on your `$PATH`).

## Building

```sh
make           # build for your arch, outputs to dist/
make build     # build for all architectures
make install   # go install ./cmd/massrepo into $GOBIN
make test      # run tests
```

## Configuration

Config lives at `~/.config/massrepo/config.yaml` and is created with defaults on first run.

```yaml
repo_path: ~/repos          # local repo cache directory
data_path: ~/.massrepo      # workspace storage directory

# Optional: Backstage integration for group resolution
backstage_url: https://api.roadie.so
backstage_token: rut_xxxxx

# Optional: static group definitions
groups:
  team:
    my-team: [org/repo1, org/repo2]
  system:
    my-system: [org/service-a, org/service-b]
```

## Quickstart

```sh
# 1. Build the Docker image
massrepo build-image

# 2. Create a workspace
massrepo create my-workspace

# 3. Open a shell with repos
massrepo shell my-workspace org/repo1 org/repo2

# Repos not yet cloned are fetched automatically from GitHub.
# Use group references to pull entire teams or systems at once:
massrepo shell my-workspace team:my-team
massrepo shell my-workspace system:booking-api

# Pull every repo owned by a GitHub org or user (via gh CLI):
massrepo shell my-workspace org:my-org
massrepo shell my-workspace user:my-handle

# Mix explicit repos and groups:
massrepo shell my-workspace system:booking-api org/some-other-repo

# 4. List sessions
massrepo list
massrepo list my-workspace   # filter to one workspace

# 5. Get the host path of a session's workspace dir (pipe into your editor)
massrepo path my-workspace/20260424-143200
$EDITOR $(massrepo path my-workspace/20260424-143200 org/repo1)
```

## Commands

### `massrepo create <name>`

Create a new workspace. Sets up persistent directories for Claude config, git credentials, and SSH keys under `~/.massrepo/workspace/<name>/data/`.

```sh
massrepo create my-workspace
massrepo create my-workspace --image massrepo-claude:latest
```

### `massrepo shell <workspace> <org/repo|group:name>...`

Create a new session and open an interactive shell inside it. At least one repo or group reference is required.

Repos are cloned from GitHub if not already in the local cache, then copied into the session. The session's container mounts the workspace's shared `data/` directory so authentication state is always available.

```sh
massrepo shell my-workspace org/my-repo
massrepo shell my-workspace org/repo1 org/repo2
massrepo shell my-workspace team:my-team
massrepo shell my-workspace system:booking-api org/extra-repo
massrepo shell my-workspace org/my-repo --shell /bin/sh
```

Repos are mounted inside the container at `/workspace/<org>/<repo>`.

#### Group references

| Form                | Resolves via       | Notes                                    |
|---------------------|--------------------|------------------------------------------|
| `team:<name>`       | static / Backstage | Configured map or Backstage owner filter |
| `system:<name>`     | static / Backstage | Configured map or Backstage system       |
| `org:<name>`        | `gh` CLI           | All repos owned by a GitHub org          |
| `user:<name>`       | `gh` CLI           | All repos owned by a GitHub user         |

`org:` and `user:` exclude archived repos and forks by default. Append
modifiers (separated by `+`) to relax the filter:

```sh
massrepo shell my-ws org:my-org+archived   # include archived repos
massrepo shell my-ws org:my-org+forks      # include forks
massrepo shell my-ws org:my-org+all        # include both
```

### `massrepo list [workspace]`

List sessions, optionally filtered to a single workspace.

```sh
massrepo list
massrepo list my-workspace
massrepo list -q    # print only workspace/session references
```

### `massrepo stop <workspace>/<session>`

Stop a running session's container without removing it.

```sh
massrepo stop my-workspace/20260424-143200
```

### `massrepo rm <workspace>[/<session>]`

Remove a workspace and all its sessions, or a single session.

```sh
massrepo rm my-workspace/20260424-143200   # remove one session
massrepo rm my-workspace                   # remove workspace and all sessions
```

### `massrepo duplicate <source> <dest>`

Create a new workspace with the same image as an existing one. No sessions are copied.

```sh
massrepo duplicate my-workspace my-workspace-2
```

### `massrepo build-image [image]`

Build (or rebuild) a Docker image. The Dockerfile is resolved from the image name:
`massrepo-claude:latest` → `<images-dir>/Dockerfile.claude`

```sh
massrepo build-image
massrepo build-image massrepo-claude:latest
```

### `massrepo path <workspace>[/<session>] [<org/repo>]`

Print the host path of a workspace, session, or repo within a session. Useful for piping into editors or shells.

```sh
massrepo path my-workspace                              # workspace root
massrepo path my-workspace/20260424-143200              # session workspace dir
massrepo path my-workspace/20260424-143200 org/repo1    # specific repo

# Open it in your editor
$EDITOR $(massrepo path my-workspace/20260424-143200)

# cd into it
cd $(massrepo path my-workspace/20260424-143200)
```

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--repos-dir` | (from config) | Override the repo cache directory |
| `--images-dir` | `./images` | Path to the directory containing Dockerfiles |
| `--image` | `massrepo-claude:latest` | Default Docker image for new workspaces |
