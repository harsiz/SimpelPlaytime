// Simpel Playtime: a lightweight playtime tracker for games and apps.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"fyne.io/fyne/v2/app"

	"github.com/harsiz/SimpelPlaytime/internal/api"
	"github.com/harsiz/SimpelPlaytime/internal/autostart"
	"github.com/harsiz/SimpelPlaytime/internal/core"
	"github.com/harsiz/SimpelPlaytime/internal/instance"
	"github.com/harsiz/SimpelPlaytime/internal/settings"
	"github.com/harsiz/SimpelPlaytime/internal/ui"
)

// version is overridden at build time with -ldflags "-X main.version=1.2.3".
var version = "1.0.0"

const appID = "io.github.harsiz.simpelplaytime"

func main() {
	minimized := flag.Bool("minimized", false, "start hidden in the tray (used by the startup entry)")
	dataFlag := flag.String("data", "", "data folder (default: %APPDATA%\\SimpelPlaytime, or a \"data\" folder next to the exe in portable mode)")
	headless := flag.Bool("headless", false, "run without a window: just tracking and the local API (Ctrl+C to stop)")
	noAPI := flag.Bool("no-api", false, "do not start the local API this run")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	_ = autostart.Flag

	if *showVersion {
		fmt.Println("Simpel Playtime", version)
		return
	}

	dir, err := dataDir(*dataFlag)
	if err != nil {
		fatal("Could not find a data folder: " + err.Error())
	}
	closeLog := setupLog(dir)
	defer closeLog()

	inst, err := instance.Acquire(dir)
	if errors.Is(err, instance.ErrAlreadyRunning) {
		// already running: ask it to show its window and get out
		if !*headless {
			if err := instance.SignalShow(dir); err != nil {
				log.Printf("could not reach the running instance: %v", err)
			}
		}
		fmt.Println("Simpel Playtime is already running.")
		return
	}
	if err != nil {
		fatal("Could not start: " + err.Error())
	}
	defer inst.Release()

	c, err := core.New(core.Options{DataDir: dir, Version: version})
	if err != nil {
		fatal("Could not open the data folder " + dir + ": " + err.Error())
	}
	log.Printf("Simpel Playtime %s starting, data in %s, %d entries, %d sessions", version, dir, len(c.Store.Entries()), c.Store.SessionCount())

	var show func()
	srv := api.New(c, func() {
		if show != nil {
			show()
		}
	})
	if !*noAPI {
		if err := srv.Apply(); err != nil {
			log.Printf("local API: %v", err)
		}
		c.Settings.OnChange(func(_, _ settings.Settings) { _ = srv.Apply() })
	}
	c.Start()
	if err := c.ApplyAutostart(); err != nil {
		log.Printf("autostart: %v", err)
	}

	if *headless {
		log.Printf("running headless, API on %s", srv.Addr())
		fmt.Printf("Simpel Playtime %s running headless. API: http://%s/api/v1  (Ctrl+C to stop)\n", version, srv.Addr())
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		srv.Stop()
		c.Stop()
		log.Printf("stopped")
		return
	}

	a := app.NewWithID(appID)
	u := ui.New(c, srv, a)
	show = u.Show
	if err := inst.Serve(u.Show); err != nil {
		log.Printf("instance server: %v", err)
	}
	startHidden := *minimized && c.Settings.Get().StartMinimized
	u.Run(startHidden)

	srv.Stop()
	c.Stop()
	log.Printf("bye")
}

// dataDir picks where everything is stored. A "data" folder (or a
// portable.txt file) next to the exe switches to portable mode.
func dataDir(flagValue string) (string, error) {
	if flagValue != "" {
		return filepath.Abs(flagValue)
	}
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		if st, err := os.Stat(filepath.Join(base, "data")); err == nil && st.IsDir() {
			return filepath.Join(base, "data"), nil
		}
		if _, err := os.Stat(filepath.Join(base, "portable.txt")); err == nil {
			return filepath.Join(base, "data"), nil
		}
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "SimpelPlaytime"), nil
}

// setupLog writes logs to simpelplaytime.log in the data folder (and to
// stderr when there is one). The log is trimmed when it grows past 1 MB.
func setupLog(dir string) func() {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return func() {}
	}
	p := filepath.Join(dir, "simpelplaytime.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
		_ = os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return func() {}
	}
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	log.SetFlags(log.LstdFlags)
	return func() { f.Close() }
}
