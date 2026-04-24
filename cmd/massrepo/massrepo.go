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
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	if cfg.RepoPath != "" {
		flagReposDir = cfg.RepoPath
	}

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

Repositories are stored under the configured repos directory (default: ./repositories)
and copied into isolated Docker containers called workspaces.`,
	Version: fmt.Sprintf("%s (%s) %s", version, commit, date),
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagReposDir, "repos-dir", "./repositories",
		"path to the repositories directory")
	rootCmd.PersistentFlags().StringVar(&flagImagesDir, "images-dir", "./images",
		"path to the directory containing Dockerfiles")
	rootCmd.PersistentFlags().StringVar(&flagImage, "image", "massrepo-claude:latest",
		"default Docker image for new workspaces")

	rootCmd.AddCommand(
		createCmd,
		listCmd,
		shellCmd,
		stopCmd,
		startCmd,
		rmCmd,
		duplicateCmd,
		buildImageCmd,
		openCmd,
	)
}

// newManager constructs a workspace.Manager using the current flag values.
func newManager() (*workspace.Manager, error) {
	reposDir, err := filepath.Abs(flagReposDir)
	if err != nil {
		return nil, fmt.Errorf("resolve repos-dir: %w", err)
	}
	imagesDir, err := filepath.Abs(flagImagesDir)
	if err != nil {
		return nil, fmt.Errorf("resolve images-dir: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	workspacesDir := filepath.Join(home, ".massrepo", "workspaces")
	return workspace.NewManager(reposDir, workspacesDir, imagesDir, flagImage)
}

// --- create ---

var createImage string

var createCmd = &cobra.Command{
	Use:   "create <name> <org/repo> [<org/repo>...]",
	Short: "Create a new workspace from one or more repositories",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, repos := args[0], args[1:]
		m, err := newManager()
		if err != nil {
			return err
		}
		img := createImage
		if img == "" {
			img = flagImage
		}
		w, err := m.Create(cmd.Context(), workspace.CreateOptions{
			Name:  name,
			Repos: repos,
			Image: img,
		})
		if err != nil {
			return err
		}
		fmt.Printf("Created workspace %q with %d repo(s) (container: %s)\n",
			w.Name, len(w.Repos), w.Container[:12])
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
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all workspaces",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager()
		if err != nil {
			return err
		}
		workspaces, err := m.List(cmd.Context())
		if err != nil {
			return err
		}
		if listQuiet {
			for _, w := range workspaces {
				fmt.Println(w.Name)
			}
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tREPOS\tSTATUS\tCREATED")
		for _, w := range workspaces {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				w.Name,
				strings.Join(w.Repos, ", "),
				w.Status,
				w.Created.Local().Format(time.DateTime),
			)
		}
		return tw.Flush()
	},
}

func init() {
	listCmd.Flags().BoolVarP(&listQuiet, "quiet", "q", false, "print only names")
}

// --- shell ---

var shellShell string

var shellCmd = &cobra.Command{
	Use:   "shell <name>",
	Short: "Open an interactive shell in a workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager()
		if err != nil {
			return err
		}
		return m.Shell(cmd.Context(), args[0], shellShell)
	},
}

func init() {
	shellCmd.Flags().StringVar(&shellShell, "shell", "/bin/bash",
		"shell executable to run inside the container")
}

// --- stop ---

var stopCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Stop a running workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager()
		if err != nil {
			return err
		}
		if err := m.Stop(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Stopped workspace %q\n", args[0])
		return nil
	},
}

// --- start ---

var startCmd = &cobra.Command{
	Use:   "start <name>",
	Short: "Start a stopped workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager()
		if err != nil {
			return err
		}
		if err := m.Start(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Started workspace %q\n", args[0])
		return nil
	},
}

// --- rm ---

var rmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a workspace and its data",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager()
		if err != nil {
			return err
		}
		if err := m.Remove(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Removed workspace %q\n", args[0])
		return nil
	},
}

// --- duplicate ---

var duplicateCmd = &cobra.Command{
	Use:   "duplicate <source> <dest>",
	Short: "Duplicate a workspace into a new independent workspace",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newManager()
		if err != nil {
			return err
		}
		w, err := m.Duplicate(cmd.Context(), args[0], args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Duplicated %q to %q (container: %s)\n", args[0], w.Name, w.Container[:12])
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
		m, err := newManager()
		if err != nil {
			return err
		}
		return m.BuildImage(cmd.Context(), imageName)
	},
}

// --- open ---

var openEditor string

var openCmd = &cobra.Command{
	Use:   "open <name> [<org/repo>]",
	Short: "Open a workspace or repo in an editor",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
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
		m, err := newManager()
		if err != nil {
			return err
		}
		w, err := m.Get(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		target := w.WorkDir
		if len(args) == 2 {
			target = filepath.Join(w.WorkDir, filepath.FromSlash(args[1]))
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
