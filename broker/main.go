// Command broker is the Peregrine lab's black-box FIX acceptor. It stands in
// for infrastructure we neither control nor instrument (AGENTS.md §28.1).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quickfixgo/quickfix"
	"github.com/quickfixgo/quickfix/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "broker: %v\n", err)
		os.Exit(1)
	}
}

// run returns errors instead of exiting, so deferred cleanup still runs.
func run() error {
	cfgPath := flag.String("config", "broker.cfg", "FIX session settings file")
	truthPath := flag.String("ground-truth", "ground-truth.jsonl",
		"where to append ground truth; never read this during diagnosis")
	profileName := flag.String("profile", "normal", "fault profile to inject")
	listProfiles := flag.Bool("list-profiles", false, "list fault profiles and exit")
	flag.Parse()

	if *listProfiles {
		fmt.Print("Fault profiles:\n", describeProfiles())
		return nil
	}

	log := newLogger()

	fault, err := lookupProfile(*profileName)
	if err != nil {
		return err
	}

	settings, err := loadSettings(*cfgPath)
	if err != nil {
		return err
	}

	// Without the answer key no scenario can be validated, so a broker that
	// cannot record one has no purpose. Fail rather than run blind.
	truth, err := newRecorder(*truthPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := truth.close(); err != nil {
			log.Error("closing ground truth", "error", err.Error())
		}
	}()

	app := newApplication(log, truth, fault)

	acceptor, err := quickfix.NewAcceptor(
		app,
		quickfix.NewMemoryStoreFactory(),
		settings,
		quickfix.NewNullLogFactory(),
	)
	if err != nil {
		return fmt.Errorf("create acceptor: %w", err)
	}

	if err := acceptor.Start(); err != nil {
		return startError(settings, err)
	}
	defer func() {
		acceptor.Stop()
		log.Info("broker stopped")
	}()

	port, _ := acceptPort(settings)
	log.Info("broker listening",
		"port", port,
		"config", *cfgPath,
		"ground_truth", *truthPath,
		"profile", fault.name,
		"ack_delay", fault.ackDelay.String())

	// Restores the default handler, so a second Ctrl-C kills a wedged process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	log.Info("shutdown signal received")

	// Give held replies a chance to go out rather than dropping orders a
	// client is still waiting on.
	drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.drain(drainCtx); err != nil {
		log.Warn("drain incomplete", "error", err.Error())
	}

	return nil
}

func loadSettings(path string) (*quickfix.Settings, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()

	settings, err := quickfix.ParseSettings(f)
	if err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return settings, nil
}

func acceptPort(settings *quickfix.Settings) (int, bool) {
	global := settings.GlobalSettings()
	if global == nil || !global.HasSetting(config.SocketAcceptPort) {
		return 0, false
	}
	port, err := global.IntSetting(config.SocketAcceptPort)
	if err != nil {
		return 0, false
	}
	return port, true
}

// startError spells out the port clash because a stale broker keeps answering:
// the next run then looks healthy while testing the wrong process.
func startError(settings *quickfix.Settings, err error) error {
	port, ok := acceptPort(settings)
	if !ok || !isAddrInUse(err) {
		return fmt.Errorf("start acceptor: %w", err)
	}

	return fmt.Errorf(`port %d is already in use.

Another broker is probably still running:

    lsof -nP -iTCP:%d -sTCP:LISTEN             # what is holding it
    kill $(lsof -nP -iTCP:%d -sTCP:LISTEN -t)  # stop it`, port, port, port)
}

// isAddrInUse falls back to the message because the errno is not always
// preserved through the engine's wrapping.
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return strings.Contains(err.Error(), "address already in use")
}

// newLogger writes JSON to stdout so logs stay parseable (AGENTS.md §19).
func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}
