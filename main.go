// Package main is the Wails (Go + WebView2) entrypoint for the OpenCode
// Odometer. It wires the app core to a frameless, always-on-top window that
// renders the dockable odometer bar and the expanded board in HTML/CSS/JS.
package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/vivek9102/opencode-odometer/internal/app"
	"github.com/vivek9102/opencode-odometer/internal/lock"
	"github.com/vivek9102/opencode-odometer/internal/prices"
	"github.com/vivek9102/opencode-odometer/internal/wservice"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func dataDir() string {
	if d := os.Getenv("OPENCODE_ODOMETER_DIR"); d != "" {
		return d
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.Getenv("XDG_DATA_HOME")
	}
	if base == "" {
		home, _ := os.UserHomeDir()
		base = home
	}
	d := filepath.Join(base, "OpenCodeOdometer")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func appDir() string {
	exe, err := os.Executable()
	if err == nil {
		if d, perr := filepath.Abs(filepath.Dir(exe)); perr == nil {
			return d
		}
	}
	wd, _ := os.Getwd()
	return wd
}

func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return exe
	}
	return abs
}

func main() {
	// Wails runs a bindings-tagged executable during builds. Reflection must
	// work while the live widget holds its lock, without installing plugins
	// or touching the user's ledger/configuration.
	if bindingsBuild {
		if err := wails.Run(&options.App{Bind: []interface{}{wservice.New()}}); err != nil {
			log.Fatalf("bindings: %v", err)
		}
		return
	}
	dir := dataDir()
	logPath := filepath.Join(dir, "opencode_odometer_events.log")
	logMsg := func(format string, args ...any) {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
			f.Close()
		}
	}

	lockFile := filepath.Join(os.TempDir(), "opencode_odometer.lock")
	ok, err := lock.Acquire(lockFile)
	if err != nil {
		logMsg("lock error: %v", err)
		log.Fatalf("lock: %v", err)
	}
	if !ok {
		logMsg("startup aborted: another instance is already running")
		log.Println("[odometer] another instance is already running (check Task Manager or stop existing process)")
		return
	}
	defer lock.Release(lockFile)

	ad := appDir()
	logMsg("odometer starting appDir=%s dataDir=%s", ad, dir)

	if err := app.WritePointer(dir, filepath.Join(dir, "budget.json"), filepath.Join(dir, "grace_claims.json"), ad, executablePath()); err != nil {
		logMsg("pointer write failed: %v", err)
	}

	svc := wservice.New()

	cfg := func() app.Config {
		// prices.json beside the exe wins; otherwise fall back to the data dir
		pricesPath := filepath.Join(ad, "prices.json")
		if _, err := os.Stat(pricesPath); err != nil {
			pricesPath = filepath.Join(dir, "prices.json")
			// Neither location has a table. Materialise the embedded snapshot
			// into the data dir so a bare executable still prices, instead of
			// reporting $0.00 for every model. The background refresh
			// overwrites it with live rates shortly after startup.
			if wrote, werr := prices.EnsureCatalog(pricesPath); werr != nil {
				logMsg("seed prices failed: %v", werr)
			} else if wrote {
				logMsg("wrote embedded price table to %s", pricesPath)
			}
		}
		overlay := filepath.Join(ad, "prices.local.json")
		if _, err := os.Stat(overlay); err != nil {
			overlay = filepath.Join(dir, "prices.local.json")
		}
		opencodeURL := os.Getenv("OPENCODE_URL")
		if opencodeURL == "" {
			opencodeURL = os.Getenv("OPENCODE_SERVER_URL")
		}
		return app.Config{
			PricesFile:       pricesPath,
			OverlayFile:      overlay,
			StateFile:        filepath.Join(dir, "odometer_state.json"),
			BudgetFile:       filepath.Join(dir, "budget.json"),
			GraceFile:        filepath.Join(dir, "grace_claims.json"),
			LogFile:          logPath,
			MaxMessages:      20000,
			OpenCodeURL:      opencodeURL,
			OpenSessionsOnly: true,
		}
	}

	a, err := app.New(cfg())
	if err != nil {
		logMsg("app.New failed: %v", err)
		log.Fatalf("init: %v", err)
	}
	svc.App = a

	// Install the plugin if OpenCode does not have it (or has an older copy).
	// Without it there is no data path at all, so a freshly copied executable
	// would run and silently report nothing.
	if st := a.EnsurePlugin(); st.Err != nil {
		logMsg("plugin install failed: %v", st.Err)
	} else if st.Installed {
		logMsg("plugin installed at %s - restart OpenCode to activate", st.Path)
	} else if st.Updated {
		logMsg("plugin updated at %s - restart OpenCode to activate", st.Path)
	}

	err = wails.Run(&options.App{
		Title:            "OpenCode Odometer",
		Width:            340,
		Height:           46,
		Frameless:        true,
		AlwaysOnTop:      true,
		StartHidden:      true,
		DisableResize:    true,
		BackgroundColour: &options.RGBA{R: 11, G: 11, B: 13, A: 255},
		CSSDragProperty:  "--wails-draggable",
		CSSDragValue:     "drag",
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: func(ctx context.Context) {
			logMsg("wails OnStartup triggered")
			svc.SetContext(ctx)
			svc.Start()
		},
		OnDomReady: func(ctx context.Context) {
			logMsg("wails OnDomReady triggered - showing window")
			svc.Ready()
		},
		Windows: &windows.Options{
			WebviewIsTransparent:              false,
			WindowIsTranslucent:               false,
			DisableFramelessWindowDecorations: true,
			Theme:                             windows.Dark,
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHidden(),
			WebviewIsTransparent: false,
			Appearance:           mac.NSAppearanceNameDarkAqua,
		},
		OnShutdown: func(ctx context.Context) { svc.Stop(); a.SaveState() },
		Bind:       []interface{}{svc},
	})
	if err != nil {
		logMsg("wails run failed: %v", err)
		log.Fatalf("wails: %v", err)
	}
}
