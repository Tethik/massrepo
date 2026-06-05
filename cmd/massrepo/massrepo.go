package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tethik/massrepo/internal/config"
	"github.com/Tethik/massrepo/internal/groups"
	"github.com/Tethik/massrepo/internal/workspace"
)

var (
	version string
	commit  string
	date    string
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// Persistent flags on the root command.
var (
	flagReposDir  string
	flagImagesDir string
	flagImage     string
)

var rootCmd = &cobra.Command{
	Use:   "massrepo",
	Short: "Run security analysis and LLM tasks across many repositories at scale",
	Long: `massrepo manages sandboxed Docker workspaces for running security analysis,
patching, and LLM tasks across many repositories simultaneously.

Each workspace holds shared authentication state. Spawning a shell creates an
independent session with its own copy of the workspace repos.`,
	Version: fmt.Sprintf("%s (%s) %s", version, commit, date),
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagReposDir, "repos-dir", "",
		"path to the repositories directory (overrides config)")
	rootCmd.PersistentFlags().StringVar(&flagImagesDir, "images-dir", "./images",
		"path to the directory containing Dockerfiles")
	rootCmd.PersistentFlags().StringVar(&flagImage, "image", "massrepo-claude:latest",
		"default Docker image for new workspaces")

	rootCmd.AddCommand(
		createCmd,
		listCmd,
		shellCmd,
		stopCmd,
		rmCmd,
		duplicateCmd,
		exportCmd,
		importCmd,
		skillCmd,
		mcpCmd,
		buildImageCmd,
		pathCmd,
	)
}

// loadConfig loads the application config and exits on error.
func loadConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

// newManager constructs a workspace.Manager using the current flag values.
func newManager(cfg *config.Config) (*workspace.Manager, error) {
	reposDir := cfg.RepoPath
	if flagReposDir != "" {
		r, err := filepath.Abs(flagReposDir)
		if err != nil {
			return nil, fmt.Errorf("resolve repos-dir: %v", err)
		}
		reposDir = r
	}

	imagesDir, err := filepath.Abs(flagImagesDir)
	if err != nil {
		return nil, fmt.Errorf("resolve images-dir: %v", err)
	}

	workspacesDir := filepath.Join(cfg.DataPath, "workspace")
	return workspace.NewManager(reposDir, workspacesDir, imagesDir, flagImage)
}

// splitRef splits "workspace/session" into its two parts.
// If there is no "/" the session part is empty.
func splitRef(ref string) (ws, session string) {
	ws, session, _ = strings.Cut(ref, "/")
	return ws, session
}

// confirm prints prompt and returns true if the user answers "y" or "yes".
func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	s := bufio.NewScanner(os.Stdin)
	if !s.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(s.Text()))
	return answer == "y" || answer == "yes"
}

// --- create ---

var (
	createImage           string
	createSkills          []string
	createMCP             []string
	createNoDefaultSkills bool
	createNoDefaultMCP    bool
)

var createCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		m, err := newManager(cfg)
		if err != nil {
			return err
		}
		img := createImage
		if img == "" {
			img = flagImage
		}
		assets, err := resolveAssets(cfg)
		if err != nil {
			return err
		}
		ws, err := m.Create(cmd.Context(), workspace.CreateOptions{
			Name:   args[0],
			Image:  img,
			Assets: assets,
		})
		if err != nil {
			return err
		}
		fmt.Printf("Created workspace %q\n", ws.Name)
		return nil
	},
}

func init() {
	createCmd.Flags().StringVar(&createImage, "image", "",
		"Docker image for this workspace (overrides --image)")
	createCmd.Flags().StringArrayVar(&createSkills, "skill", nil,
		"extra skill source directory to seed (repeatable; in addition to config defaults)")
	createCmd.Flags().StringArrayVar(&createMCP, "mcp", nil,
		"name of an MCP server (from config mcp_servers) to enable (repeatable)")
	createCmd.Flags().BoolVar(&createNoDefaultSkills, "no-default-skills", false,
		"skip the skills configured under 'skills' in config")
	createCmd.Flags().BoolVar(&createNoDefaultMCP, "no-default-mcp", false,
		"skip the servers listed under 'default_mcp_servers' in config")
}

// resolveAssets combines the configured defaults with per-create flags into the
// final set of skills and MCP servers to seed into the new workspace.
func resolveAssets(cfg *config.Config) (workspace.ClaudeAssets, error) {
	var skills []workspace.SkillSource
	if !createNoDefaultSkills {
		skills = append(skills, cfg.Skills...)
	}
	for _, p := range createSkills {
		skills = append(skills, workspace.SkillSource{Path: p})
	}

	var names []string
	if !createNoDefaultMCP {
		names = append(names, cfg.DefaultMCPServers...)
	}
	names = append(names, createMCP...)

	var servers map[string]workspace.MCPServer
	for _, name := range names {
		srv, ok := cfg.MCPServers[name]
		if !ok {
			return workspace.ClaudeAssets{}, fmt.Errorf("unknown mcp server %q: define it under mcp_servers in config", name)
		}
		if servers == nil {
			servers = make(map[string]workspace.MCPServer)
		}
		servers[name] = srv
	}

	return workspace.ClaudeAssets{Skills: skills, MCPServers: servers}, nil
}

// --- list ---

var listQuiet bool

var listCmd = &cobra.Command{
	Use:     "list [workspace]",
	Aliases: []string{"ls"},
	Short:   "List sessions, optionally filtered to a workspace",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		ws := ""
		if len(args) == 1 {
			ws = args[0]
		}
		sessions, err := m.ListSessions(cmd.Context(), ws)
		if err != nil {
			return err
		}
		if listQuiet {
			for _, s := range sessions {
				fmt.Printf("%s/%s\n", s.WorkspaceName, s.ID)
			}
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "WORKSPACE\tSESSION\tSTATUS\tCREATED")
		for _, s := range sessions {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				s.WorkspaceName,
				s.ID,
				s.Status,
				s.Created.Local().Format(time.DateTime),
			)
		}
		return tw.Flush()
	},
}

func init() {
	listCmd.Flags().BoolVarP(&listQuiet, "quiet", "q", false, "print only workspace/session references")
}

// --- shell ---

var shellShell string

// expandRefs resolves group references; plain org/repo paths pass through unchanged.
func expandRefs(ctx context.Context, r groups.Resolver, refs []string) ([]string, error) {
	var repos []string
	for _, ref := range refs {
		if groups.IsGroupRef(ref) {
			resolved, err := r.Resolve(ctx, ref)
			if err != nil {
				return nil, fmt.Errorf("resolve %q: %v", ref, err)
			}
			repos = append(repos, resolved...)
		} else {
			repos = append(repos, ref)
		}
	}
	return repos, nil
}

var shellCmd = &cobra.Command{
	Use:   "shell <workspace> <org/repo|team:name|system:name|org:name|user:name> [...]",
	Short: "Create a new session with the given repos and open an interactive shell in it",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		m, err := newManager(cfg)
		if err != nil {
			return err
		}
		resolver := groups.New(cfg.Groups, cfg.BackstageURL, cfg.BackstageToken)
		repos, err := expandRefs(cmd.Context(), resolver, args[1:])
		if err != nil {
			return err
		}
		ws := args[0]
		const largeRepoSetThreshold = 20
		if len(repos) > largeRepoSetThreshold {
			if !confirm(fmt.Sprintf("This will prepare %d repos. Continue?", len(repos))) {
				return nil
			}
		}
		sessionID, err := m.Shell(cmd.Context(), ws, repos, shellShell)
		if err != nil {
			return err
		}
		if confirm("Remove session?") {
			if err := m.RemoveSession(cmd.Context(), ws, sessionID); err != nil {
				return err
			}
			fmt.Printf("Removed session %s/%s\n", ws, sessionID)
		}
		return nil
	},
}

func init() {
	shellCmd.Flags().StringVar(&shellShell, "shell", "/bin/bash",
		"shell executable to run inside the container")
}

var stopCmd = &cobra.Command{
	Use:   "stop <workspace>[/<session>]",
	Short: "Stop a session's container, or all sessions in a workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, sess := splitRef(args[0])
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		if sess != "" {
			if err := m.StopSession(cmd.Context(), ws, sess); err != nil {
				return err
			}
			fmt.Printf("Stopped session %s/%s\n", ws, sess)
			return nil
		}
		sessions, err := m.ListSessions(cmd.Context(), ws)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			fmt.Printf("No running sessions for workspace %q\n", ws)
			return nil
		}
		fmt.Printf("Workspace %q has %d session(s):\n", ws, len(sessions))
		for _, s := range sessions {
			fmt.Printf("  %s (%s)\n", s.ID, s.Status)
		}
		if !confirm(fmt.Sprintf("Stop all %d session(s)?", len(sessions))) {
			return nil
		}
		for _, s := range sessions {
			if err := m.StopSession(cmd.Context(), ws, s.ID); err != nil {
				return err
			}
			fmt.Printf("Stopped session %s/%s\n", ws, s.ID)
		}
		return nil
	},
}

// --- rm ---

var rmCmd = &cobra.Command{
	Use:   "rm <workspace>[/<session>]",
	Short: "Remove a workspace and all its sessions, or a single session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, sess := splitRef(args[0])
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		if sess != "" {
			if err := m.RemoveSession(cmd.Context(), ws, sess); err != nil {
				return err
			}
			fmt.Printf("Removed session %s/%s\n", ws, sess)
			return nil
		}
		if err := m.Remove(cmd.Context(), ws); err != nil {
			return err
		}
		fmt.Printf("Removed workspace %q\n", ws)
		return nil
	},
}

// --- duplicate ---

var duplicateCmd = &cobra.Command{
	Use:   "duplicate <source> <dest>",
	Short: "Create a new workspace with the same configuration and image as an existing one",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		cfg, err := m.Duplicate(cmd.Context(), args[0], args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Duplicated %q to %q\n", args[0], cfg.Name)
		return nil
	},
}

// --- export ---

var exportOutput string

var exportCmd = &cobra.Command{
	Use:   "export <workspace>",
	Short: "Export a workspace's skills and MCP servers as a shareable manifest",
	Long: `Export a workspace's skills and MCP servers as a portable manifest.

Skills are emitted as git references (local-path skills are skipped with a
warning, since they are not portable). MCP secret values in env/headers are
stripped, leaving the keys as placeholders for the importer to fill in.

The manifest is written to stdout by default, or to a file with --output.`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		man, err := m.ExportManifest(args[0])
		if err != nil {
			return err
		}
		out := os.Stdout
		if exportOutput != "" {
			f, err := os.Create(exportOutput)
			if err != nil {
				return fmt.Errorf("create output file: %v", err)
			}
			defer f.Close()
			out = f
		}
		if err := workspace.WriteManifest(man, out); err != nil {
			return err
		}
		if exportOutput != "" {
			fmt.Fprintf(os.Stderr, "Wrote manifest to %s\n", exportOutput)
		}
		return nil
	},
}

func init() {
	exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "",
		"write the manifest to a file instead of stdout")
}

// --- import ---

var (
	importUpdate bool
	importPrune  bool
)

var importCmd = &cobra.Command{
	Use:   "import <manifest> <name>",
	Short: "Create or update a workspace from a shared manifest",
	Long: `Create a workspace from a manifest produced by 'massrepo export', or update
an existing one with --update.

Git-backed skills are cloned and MCP servers are seeded. Without --update the
workspace must not already exist. With --update the manifest is merged into the
existing workspace: skills and MCP servers are added or updated, the manifest's
image is adopted, and everything else is left in place — add --prune to also
remove skills/servers absent from the manifest.

Re-applying an exported manifest is safe: blank secret placeholders in the
manifest do not overwrite env/header values already filled in the workspace's
home/.claude.json.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		man, err := workspace.ReadManifest(args[0])
		if err != nil {
			return err
		}
		manifestPath, name := args[0], args[1]

		// --prune only makes sense when updating.
		update := importUpdate || importPrune
		_, err = m.Workspace(name)
		exists := err == nil

		if exists && !update {
			return fmt.Errorf("workspace %q already exists; pass --update to update it", name)
		}
		if exists {
			if err := m.ApplyManifest(cmd.Context(), name, man, workspace.ApplyOptions{Prune: importPrune}); err != nil {
				return err
			}
			fmt.Printf("Updated workspace %q from %s\n", name, manifestPath)
			return nil
		}

		img := man.Image
		if img == "" {
			img = flagImage
		}
		ws, err := m.Create(cmd.Context(), workspace.CreateOptions{
			Name:  name,
			Image: img,
			Assets: workspace.ClaudeAssets{
				Skills:     man.Skills,
				MCPServers: man.MCPServers,
			},
		})
		if err != nil {
			return err
		}
		fmt.Printf("Created workspace %q from %s\n", ws.Name, manifestPath)
		return nil
	},
}

func init() {
	importCmd.Flags().BoolVar(&importUpdate, "update", false,
		"update the workspace if it already exists instead of erroring")
	importCmd.Flags().BoolVar(&importPrune, "prune", false,
		"when updating, remove skills/servers not present in the manifest (implies --update)")
}

// --- skill ---

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage the skills provisioned in a workspace",
}

var (
	skillAddRef    string
	skillAddSubdir string
)

var skillAddCmd = &cobra.Command{
	Use:   "add <workspace> <path|git-url>",
	Short: "Add a skill to a workspace",
	Long: `Add a skill to an existing workspace.

The source is either a local directory or a git URL. For git sources, both --ref
and --subdir are required: --ref pins the revision and --subdir selects the
directory within the repository that holds the skill (use "." when the skill is
at the repository root).`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		ws, source := args[0], args[1]
		var src workspace.SkillSource
		if isGitURL(source) {
			src = workspace.SkillSource{Git: source, Ref: skillAddRef, Subdir: skillAddSubdir}
		} else {
			src = workspace.SkillSource{Path: source}
		}
		if err := src.Validate(); err != nil {
			return err
		}
		if err := m.AddSkill(cmd.Context(), ws, src); err != nil {
			return err
		}
		fmt.Printf("Added skill %q to workspace %q\n", workspace.SkillName(src), ws)
		return nil
	},
}

var skillListCmd = &cobra.Command{
	Use:     "list <workspace>",
	Aliases: []string{"ls"},
	Short:   "List the skills provisioned in a workspace",
	Args:    cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		cfg, err := m.Workspace(args[0])
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSOURCE")
		for _, s := range cfg.Skills {
			fmt.Fprintf(tw, "%s\t%s\n", workspace.SkillName(s), skillSourceString(s))
		}
		return tw.Flush()
	},
}

var skillRmCmd = &cobra.Command{
	Use:   "rm <workspace> <name>",
	Short: "Remove a skill from a workspace",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		if err := m.RemoveSkill(args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("Removed skill %q from workspace %q\n", args[1], args[0])
		return nil
	},
}

func init() {
	skillAddCmd.Flags().StringVar(&skillAddRef, "ref", "", "git ref (branch, tag, or commit); required for git sources")
	skillAddCmd.Flags().StringVar(&skillAddSubdir, "subdir", "", "directory within a git repository holding the skill (\".\" for the repo root); required for git sources")
	skillCmd.AddCommand(skillAddCmd, skillListCmd, skillRmCmd)
}

// --- mcp ---

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Manage the MCP servers provisioned in a workspace",
}

var (
	mcpAddURL       string
	mcpAddTransport string
	mcpAddHeaders   []string
	mcpAddCommand   string
	mcpAddArgs      []string
	mcpAddEnv       []string
)

var mcpAddCmd = &cobra.Command{
	Use:   "add <workspace> <name>",
	Short: "Add an MCP server to a workspace",
	Long: `Add an MCP server to an existing workspace.

Provide an HTTP server with --url (and optional --header K=V), a stdio server
with --command (and optional --arg / --env K=V), or neither to look the name up
in the 'mcp_servers' library defined in config.`,
	Args: cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		cfg := loadConfig()
		m, err := newManager(cfg)
		if err != nil {
			return err
		}
		ws, name := args[0], args[1]
		srv, err := mcpServerFromFlags(cfg, name)
		if err != nil {
			return err
		}
		if err := m.AddMCPServer(ws, name, srv); err != nil {
			return err
		}
		fmt.Printf("Added MCP server %q to workspace %q\n", name, ws)
		return nil
	},
}

var mcpListCmd = &cobra.Command{
	Use:     "list <workspace>",
	Aliases: []string{"ls"},
	Short:   "List the MCP servers provisioned in a workspace",
	Args:    cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		cfg, err := m.Workspace(args[0])
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tTYPE\tTARGET")
		for name, srv := range cfg.MCPServers {
			target := srv.URL
			if target == "" {
				target = srv.Command
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", name, srv.Type, target)
		}
		return tw.Flush()
	},
}

var mcpRmCmd = &cobra.Command{
	Use:   "rm <workspace> <name>",
	Short: "Remove an MCP server from a workspace",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		if err := m.RemoveMCPServer(args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("Removed MCP server %q from workspace %q\n", args[1], args[0])
		return nil
	},
}

func init() {
	mcpAddCmd.Flags().StringVar(&mcpAddURL, "url", "", "URL of an HTTP MCP server")
	mcpAddCmd.Flags().StringVar(&mcpAddTransport, "transport", "http", "transport for --url servers (http or sse)")
	mcpAddCmd.Flags().StringArrayVar(&mcpAddHeaders, "header", nil, "HTTP header as KEY=VALUE (repeatable)")
	mcpAddCmd.Flags().StringVar(&mcpAddCommand, "command", "", "executable for a stdio MCP server")
	mcpAddCmd.Flags().StringArrayVar(&mcpAddArgs, "arg", nil, "argument for a stdio --command (repeatable)")
	mcpAddCmd.Flags().StringArrayVar(&mcpAddEnv, "env", nil, "environment variable as KEY=VALUE (repeatable)")
	mcpCmd.AddCommand(mcpAddCmd, mcpListCmd, mcpRmCmd)
}

// mcpServerFromFlags builds an MCPServer from the add flags, or falls back to a
// named definition in the config library when no transport flags are given.
func mcpServerFromFlags(cfg *config.Config, name string) (workspace.MCPServer, error) {
	switch {
	case mcpAddURL != "":
		headers, err := parseKeyValues(mcpAddHeaders)
		if err != nil {
			return workspace.MCPServer{}, err
		}
		return workspace.MCPServer{Type: mcpAddTransport, URL: mcpAddURL, Headers: headers}, nil
	case mcpAddCommand != "":
		env, err := parseKeyValues(mcpAddEnv)
		if err != nil {
			return workspace.MCPServer{}, err
		}
		return workspace.MCPServer{Type: "stdio", Command: mcpAddCommand, Args: mcpAddArgs, Env: env}, nil
	default:
		srv, ok := cfg.MCPServers[name]
		if !ok {
			return workspace.MCPServer{}, fmt.Errorf("no --url/--command given and %q not found in config mcp_servers", name)
		}
		return srv, nil
	}
}

// isGitURL reports whether a skill source string is a remote git reference
// rather than a local path.
func isGitURL(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "git@")
}

// parseKeyValues parses "KEY=VALUE" pairs into a map.
func parseKeyValues(items []string) (map[string]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid KEY=VALUE pair %q", item)
		}
		out[key] = value
	}
	return out, nil
}

// skillSourceString renders a skill source for display in listings.
func skillSourceString(s workspace.SkillSource) string {
	if s.Git == "" {
		return s.Path
	}
	src := s.Git + "@" + s.Ref
	if s.Subdir != "" {
		src += " (" + s.Subdir + ")"
	}
	return src
}

// --- build-image ---

var buildImageCmd = &cobra.Command{
	Use:   "build-image [image]",
	Short: "Build (or rebuild) a Docker image",
	Long: `Build the Docker image used for workspaces.

If no image name is given, the value of --image is used.
The Dockerfile is resolved from the image name: "massrepo-claude:latest" uses
<images-dir>/Dockerfile.claude.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		imageName := flagImage
		if len(args) == 1 {
			imageName = args[0]
		}
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		return m.BuildImage(cmd.Context(), imageName)
	},
}

// --- path ---

var pathCmd = &cobra.Command{
	Use:   "path <workspace>[/<session>] [<org/repo>]",
	Short: "Print the host path of a workspace, session, or repo within a session",
	Long: `Print the host path of a workspace, session, or repo within a session.

  path <workspace>                         workspace root
  path <workspace>/<session>               session workspace directory
  path <workspace>/<session> <org/repo>    a specific repo within a session

Useful for piping into editors or shells, e.g. "cd $(massrepo path my-ws/abc123)".`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(_ *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}

		ws, sess := splitRef(args[0])
		cfg, err := m.Workspace(ws)
		if err != nil {
			return err
		}

		target := cfg.WorkDir
		if sess != "" {
			target = filepath.Join(cfg.WorkDir, "sessions", sess, "workspace")
		}
		if len(args) == 2 {
			target = filepath.Join(target, filepath.FromSlash(args[1]))
		}

		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("path does not exist: %s", target)
		}
		fmt.Println(target)
		return nil
	},
}
