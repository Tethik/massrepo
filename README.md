# massrepo

> [!WARNING]
> This tool is a prototype — not ready for general use.

Run security analysis, patching, and LLM tasks across many repositories at
once. Each task gets a sandboxed Docker session with its own copy of every
repo you name — one by one, or a whole team, system, or GitHub org.

## Features ✨

- **Fan out over groups** — `org:my-org`, `team:my-team`, `system:booking-api`
  expand to every repo they cover, resolved from GitHub or Backstage.
- **Sandboxed, disposable sessions** — each one is a container with its own
  copy of the repos, so several can work on the same repo independently.
- **Persistent workspaces** — Claude config, git credentials, and SSH keys
  survive across sessions, so you authenticate once.
- **Batteries for agents** — seed Claude skills and MCP servers into every
  workspace, and share a whole setup with a colleague as one YAML file.
- **Local repo cache** — repos are cloned once and reused by every session.

## Usage 🚀

Needs Docker, an authenticated [`gh`](https://cli.github.com/), and an SSH key
with GitHub access.

```sh
go install github.com/Tethik/massrepo/cmd/massrepo@latest
```

```sh
# 1. Build the Docker image
massrepo build-image

# 2. Create a workspace — a named home for auth state and agent config
massrepo create my-workspace

# 3. Open a shell with the repos you want to work on
massrepo shell my-workspace org/repo1 org/repo2
massrepo shell my-workspace team:my-team        # ...or a whole group
massrepo shell my-workspace org:my-org          # ...or a whole GitHub org

# 4. See what's running, and find it on disk
massrepo list
$EDITOR $(massrepo path my-workspace/20260424-143200 org/repo1)
```

Repos are cloned from GitHub on demand and mounted at `/workspace/<org>/<repo>`
inside the container. Full command reference: [docs/commands.md](docs/commands.md).

## Configuration ⚙️

Config lives at `~/.config/massrepo/config.yaml`, created with defaults on
first run.

| Key | Default | Description |
| --- | --- | --- |
| `repo_path` | `~/repos` | Local repo cache directory |
| `data_path` | `~/.massrepo` | Workspace storage directory |
| `groups` | — | Static `team:` / `system:` group definitions |
| `backstage_url` / `backstage_token` | — | Resolve groups from a Backstage catalog |
| `skills` | — | Claude skills seeded into every new workspace |
| `mcp_servers` / `default_mcp_servers` | — | MCP server library, and which to enable by default |

See [docs/configuration.md](docs/configuration.md) for the full file, group
references, and how skills and MCP servers are seeded.

## Docs 📚

- [Commands](docs/commands.md) — every subcommand and flag
- [Configuration](docs/configuration.md) — config file, groups, skills, MCP servers
- [Images](docs/images.md) — how workspace images are built and customized
- [Development](Development.md) — building and releasing
