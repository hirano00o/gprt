// Command gprt is a terminal UI for working with GitHub pull requests.
// Run it with no arguments to open the TUI (see the flags below); it reuses
// gh's own authentication (GH_TOKEN/GITHUB_TOKEN, or gh's stored
// config/keyring).
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/hirano00o/gprt/internal/browser"
	"github.com/hirano00o/gprt/internal/cache"
	"github.com/hirano00o/gprt/internal/config"
	"github.com/hirano00o/gprt/internal/gh"
	"github.com/hirano00o/gprt/internal/logging"
	"github.com/hirano00o/gprt/internal/store"
	"github.com/hirano00o/gprt/internal/ui"
	"github.com/hirano00o/gprt/internal/ui/keys"
	"github.com/hirano00o/gprt/internal/ui/theme"
)

// version is gprt's version string, overridden at build time with
// -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to config.yaml (default: gprt's config directory)")
	debug := flag.Bool("debug", os.Getenv("GPRT_DEBUG") != "", "enable debug logging to <state dir>/gprt.log")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("gprt " + version)
		return nil
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}

	logger, closeLog, err := setUpLogging(*debug)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeLog(); closeErr != nil {
			fmt.Fprintln(os.Stderr, "gprt: failed to close debug log:", closeErr)
		}
	}()

	theme.Apply()

	cacheDir, err := config.CacheDir()
	if err != nil {
		return err
	}
	cacheStore, err := cache.New(cacheDir)
	if err != nil {
		return err
	}

	ghClient, err := gh.New(gh.Options{Host: cfg.Host, Debug: *debug, Logger: logger})
	if err != nil {
		return err
	}

	// app is assigned once ui.New returns, below — see its own doc comment
	// for why the Store's Dispatch closure has to work the same way despite
	// the App not existing yet when these closures are created. A launcher
	// exit failure can only happen after the user has pressed "o" inside a
	// running app, by which point app is always non-nil; the nil check
	// guards only the (never actually reached) case of an exit landing
	// before that.
	var app *ui.App
	opener := browser.New(cfg.Browser, func(launchErr error) {
		if app == nil {
			logger.Warn("browser launcher exited with an error", "err", launchErr)
			return
		}
		app.ShowError("browser: " + launchErr.Error())
	})

	km, err := keys.Merge(keys.Defaults(), cfg.Keys)
	if err != nil {
		return err
	}

	st := store.New(store.Deps{
		GitHub:   ghClient,
		Cache:    cacheStore,
		Dispatch: func(f func()) { app.Dispatch(f) },
		Logger:   logger,
		Config:   cfg,
		Host:     ghClient.Host(),
	})

	app = ui.New(ui.Deps{
		Store:   st,
		Config:  cfg,
		Keymap:  km,
		Icons:   theme.IconsFor(cfg.Icons),
		Browser: opener,
		Logger:  logger,
		Recent:  recentMessages(logger),
		Version: version,
	})

	return app.Run()
}

// loadConfig resolves path and loads it. An empty path (the "--config" flag
// left unset) falls back to config.DefaultPath and keeps config.Load's own
// "missing file means defaults" behaviour. An explicit path that does not
// exist is a startup error instead: a user who typed "--config" clearly
// meant to point at a real file, and silently falling back to defaults
// would hide a typo rather than reporting it.
func loadConfig(path string) (config.Config, error) {
	if path == "" {
		defaultPath, err := config.DefaultPath()
		if err != nil {
			return config.Config{}, err
		}
		path = defaultPath
		return config.Load(path)
	}

	// Lstat, not Stat: a broken symlink is a real directory entry (Lstat
	// succeeds), and config.Load already reports that case with its own,
	// more specific error — only a path with no entry at all here (Lstat
	// itself fails) is treated as "the user's --config path is wrong".
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return config.Config{}, fmt.Errorf("config %s: no such file", path)
		}
		return config.Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return config.Load(path)
}

// setUpLogging builds gprt's logger, rooted at the state directory.
func setUpLogging(debug bool) (*slog.Logger, func() error, error) {
	stateDir, err := config.StateDir()
	if err != nil {
		return nil, nil, err
	}
	return logging.Setup(logging.Options{Debug: debug, Dir: stateDir})
}

// recentMessages returns a func suitable for ui.Deps.Recent: the logger's
// underlying Recorder's Recent method when one is present (it always is,
// per logging.Setup), or a func returning nil otherwise, so a misconfigured
// logger never causes a nil-pointer panic when the ":messages" command
// runs.
func recentMessages(logger *slog.Logger) func() []logging.Entry {
	if recorder, ok := logger.Handler().(*logging.Recorder); ok {
		return recorder.Recent
	}
	return func() []logging.Entry { return nil }
}
