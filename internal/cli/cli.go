package cli

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"

	"myworktree/internal/app"
	"myworktree/internal/config"
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
		case "config":
			if err := configCmd(logger, args[2:]); err != nil {
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
	var portalPort int

	defaultOpen := filepath.Base(strings.TrimSpace(prog)) == "mw"

	fs.StringVar(&listen, "listen", "0.0.0.0:0", "listen address")
	fs.StringVar(&auth, "auth", "", "auth token (auto-generated if not provided)")
	fs.StringVar(&tlsCert, "tls-cert", "", "path to TLS certificate PEM")
	fs.StringVar(&tlsKey, "tls-key", "", "path to TLS private key PEM")
	fs.BoolVar(&open, "open", defaultOpen, "open browser")
	fs.StringVar(&worktreesDir, "worktrees-dir", "", "worktrees root dir (default: sibling <repo>-myworktree; set to 'data' for legacy DataDir/worktrees)")
	fs.IntVar(&portalPort, "portal-port", 12345, "portal port (0=disabled)")
	_ = fs.Parse(args)

	auth = resolveGlobalAuthToken(auth, logger)

	cfg := app.Config{
		ListenAddr:   listen,
		AuthToken:    auth,
		TLSCert:      tlsCert,
		TLSKey:       tlsKey,
		Open:         open,
		WorktreesDir: worktreesDir,
		PortalPort:   portalPort,
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
	if auth != "" {
		fmt.Printf("\nRemote access token: %s\n", auth)
	}
	fmt.Println("\nPress 'o' + Enter to open browser")

	go func() {
		br := bufio.NewReader(os.Stdin)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				logger.Printf("stdin closed, browser shortcut disabled")
				return
			}
			trimmed := strings.TrimSpace(line)
			if trimmed == "o" || trimmed == "O" {
				_ = app.OpenURL(url)
				fmt.Println("Opening browser...")
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nShutting down...")
	srv.Shutdown()
	return nil
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
		Root:    root,
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

func configCmd(logger *log.Logger, args []string) error {
	if len(args) == 0 {
		return configInteractive(logger)
	}
	switch args[0] {
	case "set-auth":
		return configSetAuth(logger)
	case "get-auth":
		return configGetAuth()
	case "clear-auth":
		return configClearAuth()
	default:
		return fmt.Errorf("unknown config subcommand: %s", args[0])
	}
}

func configInteractive(logger *log.Logger) error {
	fmt.Println("Global auth configuration")
	fmt.Println("  [1] Set auth token")
	fmt.Println("  [2] View auth token")
	fmt.Println("  [3] Clear auth token")
	fmt.Println("  [q] Quit")
	fmt.Print("Choose an option: ")

	br := bufio.NewReader(os.Stdin)
	line, _, err := br.ReadLine()
	if err != nil {
		return err
	}
	switch strings.TrimSpace(string(line)) {
	case "1":
		return configSetAuth(logger)
	case "2":
		return configGetAuth()
	case "3":
		return configClearAuth()
	case "q", "Q":
		return nil
	default:
		fmt.Println("Invalid option.")
		return nil
	}
}

func configSetAuth(logger *log.Logger) error {
	fmt.Print("Enter auth token: ")
	token, err := readPassword()
	if err != nil {
		return fmt.Errorf("failed to read token: %w", err)
	}
	fmt.Print("Confirm auth token: ")
	token2, err := readPassword()
	if err != nil {
		return fmt.Errorf("failed to read token: %w", err)
	}
	if token != token2 {
		fmt.Println("Tokens do not match. Please try again.")
		return nil
	}
	gc := &config.GlobalConfig{AuthToken: token}
	if err := config.Save(gc); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Auth token saved successfully.")
	return nil
}

func configGetAuth() error {
	gc, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if gc.AuthToken == "" {
		fmt.Println("No auth token configured.")
		return nil
	}
	token := gc.AuthToken
	if len(token) >= 8 {
		fmt.Printf("Auth token: %s****%s\n", token[:4], token[len(token)-4:])
	} else {
		fmt.Println("Auth token: ****")
	}
	return nil
}

func configClearAuth() error {
	gc := &config.GlobalConfig{AuthToken: ""}
	if err := config.Save(gc); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Auth token cleared.")
	return nil
}

func readPassword() (string, error) {
	data, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	fmt.Println()
	return string(data), nil
}

// resolveGlobalAuthToken returns the effective auth token.
// If auth is provided via flag, it is used as-is.
// Otherwise, it reads from global config; if empty, a new 32-char hex token
// is generated via crypto/rand and persisted to global config.
func resolveGlobalAuthToken(auth string, logger *log.Logger) string {
	if auth == "" {
		gc, err := config.Load()
		if err != nil {
			if logger != nil {
				logger.Printf("[config] failed to load auth config: %v", err)
			}
		} else if gc.AuthToken != "" {
			return gc.AuthToken
		}

		token, err := generateAuthToken()
		if err != nil {
			if logger != nil {
				logger.Printf("[config] failed to generate auth token: %v", err)
			}
			return ""
		}
		gc.AuthToken = token
		if err := config.Save(gc); err != nil {
			if logger != nil {
				logger.Printf("[config] failed to persist auth token (session only, will not survive restart): %v", err)
			}
			return token
		}
		return token
	}
	return auth
}

func generateAuthToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
