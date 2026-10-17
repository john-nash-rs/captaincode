package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"example.com/agentbook/config"
	"example.com/agentbook/core"
	"example.com/agentbook/provider"
	"example.com/agentbook/sandbox"
	"example.com/agentbook/storage"
	"example.com/agentbook/toolbox"
	"example.com/agentbook/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agentbook:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "worker" {
		var envelope sandbox.Envelope
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
		if err != nil {
			return err
		}
		if len(data) > 65536 {
			return errors.New("worker request too large")
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(envelope.Registry.Execute(context.Background(), envelope.Call))
	}
	workspace := flag.String("workspace", "", "project directory (required)")
	databasePath := flag.String("db", "", "SQLite path outside the project")
	configPath := flag.String("config", "", "explicit user configuration JSON")
	providerName := flag.String("provider", "fixture", "fixture or openrouter")
	question := flag.String("question", "What is the project version?", "user request")
	resume := flag.String("resume", "", "resume an interrupted run by ID")
	tui := flag.Bool("tui", false, "open terminal interface")
	stream := flag.Bool("stream", false, "assemble streamed provider responses")
	flag.Parse()
	if *workspace == "" {
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
		*databasePath = filepath.Join(directory, "agentbook", "sessions.db")
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
	var languageModel core.Model = core.Fixture{}
	switch *providerName {
	case "fixture":
		fmt.Fprintln(os.Stderr, "Deterministic fixture: supports the package.json version task only.")
	case "openrouter":
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			return errors.New("set OPENROUTER_API_KEY")
		}
		if cfg.Model == "" {
			return errors.New("set model in your config")
		}
		adapter := provider.New(key, cfg.Model, "")
		adapter.Stream = *stream
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
			Model:         cfg.Model,
			Workspace:     *workspace,
			AllowWrites:   cfg.AllowWrites,
			AllowCommands: cfg.AllowCommands,
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
