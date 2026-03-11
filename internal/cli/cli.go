package cli

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"myworktree/internal/app"
	"myworktree/internal/gitx"
	"myworktree/internal/instance"
	"myworktree/internal/store"
	"myworktree/internal/tag"
	"myworktree/internal/version"
	"myworktree/internal/worktree"
)

// Run executes the CLI. args should be os.Args.
// It returns an exit code suitable for os.Exit.
func Run(args []string, logger *log.Logger) int {
	if len(args) >= 2 {
		switch args[1] {
		case "version", "--version", "-version":
			fmt.Println(version.Info(filepath.Base(strings.TrimSpace(args[0]))))
			return 0
		case "worktree":
			if err := worktreeCmd(logger, args[2:]); err != nil {
				logger.Print(err)
				return 1
			}
			return 0
		case "tag":
			if err := tagCmd(logger, args[2:]); err != nil {
				logger.Print(err)
				return 1
			}
			return 0
		case "instance":
			if err := instanceCmd(logger, args[2:]); err != nil {
				logger.Print(err)
				return 1
			}
			return 0
		}
	}

	if err := startCmd(logger, args[0], args[1:]); err != nil {
		logger.Print(err)
		return 1
	}
	return 0
}

func startCmd(logger *log.Logger, prog string, args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)

	var listen string
	var auth string
	var tlsCert string
	var tlsKey string
	var open bool
	var worktreesDir string

	defaultOpen := filepath.Base(strings.TrimSpace(prog)) == "mw"

	fs.StringVar(&listen, "listen", "127.0.0.1:0", "listen address")
	fs.StringVar(&auth, "auth", "", "auth token (required for non-loopback listen)")
	fs.StringVar(&tlsCert, "tls-cert", "", "path to TLS certificate PEM")
	fs.StringVar(&tlsKey, "tls-key", "", "path to TLS private key PEM")
	fs.BoolVar(&open, "open", defaultOpen, "open browser")
	fs.StringVar(&worktreesDir, "worktrees-dir", "", "worktrees root dir (default: sibling <repo>-myworktree; set to 'data' for legacy DataDir/worktrees)")
	_ = fs.Parse(args)

	cfg := app.Config{
		ListenAddr:   listen,
		AuthToken:    auth,
		TLSCert:      tlsCert,
		TLSKey:       tlsKey,
		Open:         open,
		WorktreesDir: worktreesDir,
	}

	srv, err := app.New(cfg, logger)
	if err != nil {
		return err
	}

	url, err := srv.Start()
	if err != nil {
		return err
	}
	// Print a friendly message with the server URL
	fmt.Println("Server running at:")
	fmt.Printf("  %s\n", url)
	fmt.Println("\nPress Ctrl+C to stop the server")

	select {}
}

func worktreeCmd(logger *log.Logger, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: myworktree worktree <new|list|delete> ...")
	}
	root, err := gitx.GitRoot(".")
	if err != nil {
		return err
	}
	dataDir, err := projectDataDir(root)
	if err != nil {
		return err
	}

	mgr := worktree.Manager{
		GitRoot: root,
		DataDir: dataDir,
		Store:   store.FileStore{Path: filepath.Join(dataDir, "state.json")},
	}

	switch args[0] {
	case "list":
		wts, err := mgr.List()
		if err != nil {
			return err
		}
		for _, wt := range wts {
			fmt.Printf("%s\t%s\t%s\t%s\n", wt.ID, wt.Name, wt.Branch, wt.Path)
		}
		return nil

	case "new":
		fs := flag.NewFlagSet("worktree new", flag.ContinueOnError)
		var base string
		var worktreesDir string
		fs.StringVar(&base, "base", "", "base ref (default: current HEAD)")
		fs.StringVar(&worktreesDir, "worktrees-dir", "", "worktrees root dir (default: sibling <repo>-myworktree; set to 'data' for legacy DataDir/worktrees)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		desc := strings.TrimSpace(strings.Join(fs.Args(), " "))
		mgr.WorktreesDir = worktreesDir
		wt, err := mgr.Create(desc, base)
		if err != nil {
			return err
		}
		fmt.Printf("created\t%s\t%s\t%s\n", wt.ID, wt.Branch, wt.Path)
		return nil

	case "import":
		if len(args) < 2 {
			return fmt.Errorf("usage: myworktree worktree import <name>")
		}
		wt, err := mgr.Import(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("imported\t%s\t%s\t%s\n", wt.ID, wt.Branch, wt.Path)
		return nil

	case "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: myworktree worktree delete <id>")
		}
		return mgr.Delete(args[1])

	default:
		return fmt.Errorf("unknown worktree subcommand: %s", args[0])
	}
}

func tagCmd(logger *log.Logger, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: myworktree tag <list>")
	}
	root, err := gitx.GitRoot(".")
	if err != nil {
		return err
	}
	dataDir, err := projectDataDir(root)
	if err != nil {
		return err
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	mgr := tag.Manager{
		GlobalPath:  filepath.Join(base, "myworktree", "tags.json"),
		ProjectPath: filepath.Join(dataDir, "tags.json"),
	}

	switch args[0] {
	case "list":
		m, err := mgr.LoadMerged()
		if err != nil {
			return err
		}
		for id, t := range m {
			fmt.Printf("%s\t%s\n", id, t.Command)
		}
		return nil
	default:
		return fmt.Errorf("unknown tag subcommand: %s", args[0])
	}
}

func instanceCmd(logger *log.Logger, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: myworktree instance <start|list|stop>")
	}
	root, err := gitx.GitRoot(".")
	if err != nil {
		return err
	}
	dataDir, err := projectDataDir(root)
	if err != nil {
		return err
	}
	mgr := &instance.Manager{
		DataDir: dataDir,
		Store:   store.FileStore{Path: filepath.Join(dataDir, "state.json")},
		Logger:  logger,
	}

	switch args[0] {
	case "list":
		items, err := mgr.List()
		if err != nil {
			return err
		}
		for _, it := range items {
			fmt.Printf("%s\t%s\t%s\t%d\t%s\n", it.ID, it.WorktreeID, it.TagID, it.PID, it.Status)
		}
		return nil

	case "start":
		fs := flag.NewFlagSet("instance start", flag.ContinueOnError)
		var worktreeID string
		var tagID string
		var command string
		var name string
		fs.StringVar(&worktreeID, "worktree", "", "managed worktree id")
		fs.StringVar(&tagID, "tag", "", "tag id (optional if --cmd is set)")
		fs.StringVar(&command, "cmd", "", "ad-hoc command (optional if --tag is set)")
		fs.StringVar(&name, "name", "", "instance display name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		item, err := mgr.Start(instance.StartInput{
			WorktreeID: worktreeID,
			TagID:      tagID,
			Command:    command,
			Name:       name,
		})
		if err != nil {
			return err
		}
		fmt.Printf("started\t%s\t%d\t%s\n", item.ID, item.PID, item.Status)
		return nil

	case "stop":
		if len(args) < 2 {
			return fmt.Errorf("usage: myworktree instance stop <id>")
		}
		return mgr.Stop(args[1])
	default:
		return fmt.Errorf("unknown instance subcommand: %s", args[0])
	}
}

func projectDataDir(gitRoot string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	repoHash := gitx.HashPath(gitRoot)
	return filepath.Join(base, "myworktree", repoHash), nil
}
