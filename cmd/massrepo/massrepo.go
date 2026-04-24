package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tethik/massrepo/internal/config"
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
		buildImageCmd,
		openCmd,
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

// --- create ---

var createImage string

var createCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		img := createImage
		if img == "" {
			img = flagImage
		}
		cfg, err := m.Create(cmd.Context(), workspace.CreateOptions{
			Name:  args[0],
			Image: img,
		})
		if err != nil {
			return err
		}
		fmt.Printf("Created workspace %q\n", cfg.Name)
		return nil
	},
}

func init() {
	createCmd.Flags().StringVar(&createImage, "image", "",
		"Docker image for this workspace (overrides --image)")
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

var shellCmd = &cobra.Command{
	Use:   "shell <workspace> <org/repo> [<org/repo>...]",
	Short: "Create a new session with the given repos and open an interactive shell in it",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		return m.Shell(cmd.Context(), args[0], args[1:], shellShell)
	},
}

func init() {
	shellCmd.Flags().StringVar(&shellShell, "shell", "/bin/bash",
		"shell executable to run inside the container")
}

var stopCmd = &cobra.Command{
	Use:   "stop <workspace>/<session>",
	Short: "Stop a session's container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, sess := splitRef(args[0])
		if sess == "" {
			return fmt.Errorf("expected <workspace>/<session>, got %q", args[0])
		}
		m, err := newManager(loadConfig())
		if err != nil {
			return err
		}
		if err := m.StopSession(cmd.Context(), ws, sess); err != nil {
			return err
		}
		fmt.Printf("Stopped session %s/%s\n", ws, sess)
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

// --- open ---

var openEditor string

var openCmd = &cobra.Command{
	Use:   "open <workspace>[/<session>] [<org/repo>]",
	Short: "Open a workspace or session directory in an editor",
	Long: `Open a workspace or session directory in an editor.

  open <workspace>                         opens the workspace root
  open <workspace>/<session>               opens the session repos directory
  open <workspace>/<session> <org/repo>    opens a specific repo within a session`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(_ *cobra.Command, args []string) error {
		editor := openEditor
		if editor == "" {
			editor = os.Getenv("VISUAL")
		}
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}
		if editor == "" {
			return fmt.Errorf("no editor configured: set --editor, $VISUAL, or $EDITOR")
		}

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
			target = filepath.Join(cfg.WorkDir, "sessions", sess, "repos")
		}
		if len(args) == 2 {
			target = filepath.Join(target, filepath.FromSlash(args[1]))
		}

		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("path does not exist: %s", target)
		}
		c := exec.Command(editor, target)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

func init() {
	openCmd.Flags().StringVar(&openEditor, "editor", "", "editor command to use (overrides $VISUAL/$EDITOR)")
}
