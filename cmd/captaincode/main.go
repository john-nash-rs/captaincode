// Package main is the CaptainCode command-line entry point.
//
// This file holds argument handling and top-level wiring only. Subcommands
// that need to report on the environment live in their own file so the
// critical worker path below stays short and auditable.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/john-nash-rs/captaincode/config"
	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/keyring"
	"github.com/john-nash-rs/captaincode/provider"
	"github.com/john-nash-rs/captaincode/sandbox"
	"github.com/john-nash-rs/captaincode/storage"
	"github.com/john-nash-rs/captaincode/toolbox"
	"github.com/john-nash-rs/captaincode/ui"
)

// version is the release identifier. Release builds overwrite it with the
// linker flag -X main.version=<tag>; a plain "go build" leaves it as "dev" so
// an unreleased binary is never mistaken for a published one.
var version = "dev"

// commit is set the same way and identifies the exact source revision.
var commit = "unknown"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "captaincode:", err)
		os.Exit(1)
	}
}

// run dispatches a subcommand, then falls back to the one-shot and terminal
// interfaces.
//
// The worker subcommand must stay first and must stay reachable without any
// flag parsing. The sandbox re-executes this same binary to run a tool, and it
// passes no arguments other than "worker". A regression here would break every
// tool execution while leaving the rest of the suite green.
func run() error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "worker":
			return runWorker()
		case "doctor":
			return runDoctor(os.Args[2:])
		case "version":
			printVersion(os.Stdout)
			return nil
		}
	}

	workspace := flag.String("workspace", "", "project directory (required)")
	databasePath := flag.String("db", "", "SQLite path outside the project")
	configPath := flag.String("config", "", "explicit user configuration JSON")
	providerName := flag.String("provider", "fixture", "fixture or openrouter")
	question := flag.String("question", "What is the project version?", "user request")
	resume := flag.String("resume", "", "resume an interrupted run by ID")
	tui := flag.Bool("tui", false, "open terminal interface")
	stream := flag.Bool("stream", false, "assemble streamed provider responses")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		printVersion(os.Stdout)
		return nil
	}
	if *workspace == "" {
		flag.Usage()
		return errors.New("provide --workspace with a project you intend to expose")
	}
	root, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *databasePath == "" {
		directory, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		*databasePath = filepath.Join(directory, "captaincode", "sessions.db")
	}
	dbPath, err := filepath.Abs(*databasePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return err
	}
	canonicalParent, err := filepath.EvalSymlinks(filepath.Dir(dbPath))
	if err != nil {
		return err
	}
	dbPath = filepath.Join(canonicalParent, filepath.Base(dbPath))
	if existing, resolveErr := filepath.EvalSymlinks(dbPath); resolveErr == nil {
		dbPath = existing
	}
	if relative, err := filepath.Rel(root, dbPath); err == nil && filepath.IsLocal(relative) {
		return errors.New("database must be outside the tool workspace")
	}
	database, err := storage.Open(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Bind(context.Background(), root); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// Built before the provider so the model can be read per request, which is
	// what lets /openrouter model apply without a restart.
	settings, err := newSettings(*configPath, *providerName, cfg.Model)
	if err != nil {
		return err
	}
	var languageModel core.Model = core.Fixture{}
	switch *providerName {
	case "fixture":
		fmt.Fprintln(os.Stderr, "Deterministic fixture: supports the package.json version task only.")
	case "openrouter":
		// A missing credential is not fatal here. The interface can store one
		// with /openrouter api-key, so refusing to start would leave no way to
		// supply it. The provider reports the failure when a request needs it.
		if _, err := keyring.Resolve(); err != nil {
			return err
		}
		// Both the credential and the model are resolved per request, so
		// /openrouter api-key and /openrouter model take effect immediately.
		adapter := provider.NewWithKey(
			func(context.Context) (string, error) { return keyring.Resolve() },
			settings.Model(), "")
		adapter.Stream = *stream
		adapter.ModelFunc = settings.Model
		languageModel = adapter
	default:
		return errors.New("unknown provider")
	}
	worker := sandbox.Worker{Executable: executable, SandboxBinary: cfg.SandboxBinary, Registry: toolbox.Registry{Root: root, MaxBytes: cfg.OutputBytes, AllowWrites: cfg.AllowWrites, AllowCommands: cfg.AllowCommands}}
	runOne := func(parent context.Context, prompt string, emit func(core.Event)) (string, error) {
		ctx, cancel := context.WithTimeout(parent, time.Duration(cfg.RunTimeoutSeconds)*time.Second)
		defer cancel()
		engine := core.Engine{Model: languageModel, Tools: worker, Store: database, MaxTurns: cfg.MaxTurns, MaxToolCalls: cfg.MaxToolCalls, MaxContextBytes: cfg.ContextBytes, ToolTimeout: time.Duration(cfg.ToolTimeoutSeconds) * time.Second, Emit: emit}
		identifier := make([]byte, 16)
		if _, err := rand.Read(identifier); err != nil {
			return "", err
		}
		state := core.NewRun(hex.EncodeToString(identifier), prompt)
		if *resume != "" {
			loaded, loadErr := database.Load(ctx, *resume)
			if loadErr != nil {
				return "", loadErr
			}
			state = loaded
			*resume = ""
			if err := engine.Recover(&state); err != nil {
				return "", err
			}
		}
		if err := engine.Run(ctx, &state); err != nil {
			return "", fmt.Errorf("run %s: %w", state.ID, err)
		}
		return fmt.Sprintf("%s\nRun: %s", state.Messages[len(state.Messages)-1].Content, state.ID), nil
	}
	if *tui {
		// The interface reports the resolved provider, model, and grants so it
		// never has to infer them. Passing them keeps that dependency explicit.
		return ui.Run(runOne, ui.Info{
			Provider:      *providerName,
			Model:         settings.Model(),
			Workspace:     *workspace,
			AllowWrites:   cfg.AllowWrites,
			AllowCommands: cfg.AllowCommands,
			Settings:      settings,
		})
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	answer, err := runOne(ctx, *question, func(event core.Event) {
		fmt.Fprintf(os.Stderr, "run=%s event=%s call=%s tool=%s duration_ms=%d\n", event.RunID, event.Kind, event.CallID, event.Tool, event.Duration.Milliseconds())
	})
	if err != nil {
		return err
	}
	fmt.Println(answer)
	return nil
}
