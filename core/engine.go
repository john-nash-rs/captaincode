package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (e *Engine) save(run *Run, kind string) error {
	// Persist an observation even if the user cancelled as it arrived.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Store.Save(ctx, *run, kind); err != nil {
		return err
	}
	if e.Emit != nil {
		e.Emit(Event{RunID: run.ID, Kind: kind})
	}
	return nil
}

func (e *Engine) stop(run *Run, phase string, cause error) error {
	run.Phase, run.Reason = phase, cause.Error()
	if err := e.save(run, phase); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// Recover must be called only while the application's exclusive run lock is held.
// An interrupted mutation remains uncertain until an operator reconciles it.
func (e *Engine) Recover(run *Run) error {
	if run.Phase != "tools" {
		return nil
	}
	for index := range run.Pending {
		pending := &run.Pending[index]
		if pending.Result != nil && pending.Result.Code == "uncertain" {
			return e.stop(run, "blocked", ErrUncertain)
		}
		if pending.State == "running" {
			if !e.Tools.ReplaySafe(pending.Call.Name) {
				pending.State = "unknown"
				return e.stop(run, "blocked", ErrUncertain)
			}
			pending.State = "pending"
		}
	}
	return e.save(run, "recovered")
}

func (e *Engine) Run(ctx context.Context, run *Run) error {
	if e.MaxTurns < 1 || e.MaxToolCalls < 1 || e.MaxContextBytes < 256 || e.ToolTimeout <= 0 {
		return errors.New("invalid engine limits")
	}
	if e.Model == nil || e.Tools == nil || e.Store == nil {
		return errors.New("missing engine dependency")
	}
	if run.Phase == "completed" {
		return nil
	}
	if run.Phase == "cancelled" || run.Phase == "failed" || run.Phase == "blocked" {
		return fmt.Errorf("run is terminal: %s", run.Phase)
	}
	for {
		if err := ctx.Err(); err != nil {
			return e.stop(run, "cancelled", err)
		}
		switch run.Phase {
		case "model":
			if run.Turns >= e.MaxTurns {
				return e.stop(run, "failed", ErrBudget)
			}
			history, err := SelectContext(run.Messages, e.MaxContextBytes)
			if err != nil {
				return e.stop(run, "failed", err)
			}
			run.Turns++
			if err := e.save(run, "model_started"); err != nil {
				return err
			}
			started := time.Now()
			reply, err := e.Model.Complete(ctx, history, e.Tools.Definitions())
			if e.Emit != nil {
				e.Emit(Event{RunID: run.ID, Kind: "model_finished", Duration: time.Since(started)})
			}
			if err != nil {
				if ctx.Err() != nil {
					return e.stop(run, "cancelled", ctx.Err())
				}
				return e.stop(run, "failed", fmt.Errorf("model request failed: %w", err))
			}
			if err := validateReply(run, reply); err != nil {
				return e.stop(run, "failed", err)
			}
			if run.ToolCalls+len(reply.Calls) > e.MaxToolCalls {
				return e.stop(run, "failed", ErrBudget)
			}
			run.Messages = append(run.Messages, reply)
			if len(reply.Calls) == 0 {
				run.Phase = "completed"
				return e.save(run, "completed")
			}
			run.Pending = nil
			for _, call := range reply.Calls {
				run.Pending = append(run.Pending, Pending{Call: call, State: "pending"})
			}
			run.Phase = "tools"
			if err := e.save(run, "tools_planned"); err != nil {
				return err
			}
		case "tools":
			for index := range run.Pending {
				pending := &run.Pending[index]
				if pending.State == "completed" {
					if pending.Result == nil || pending.Result.Code == "uncertain" {
						return e.stop(run, "blocked", ErrUncertain)
					}
					continue
				}
				if pending.State != "pending" {
					return e.stop(run, "blocked", ErrUncertain)
				}
				if err := ctx.Err(); err != nil {
					return e.stop(run, "cancelled", err)
				}
				if run.ToolCalls >= e.MaxToolCalls {
					return e.stop(run, "failed", ErrBudget)
				}
				pending.State = "running"
				run.ToolCalls++
				if err := e.save(run, "tool_started"); err != nil {
					return err
				}
				toolCtx, cancel := context.WithTimeout(ctx, e.ToolTimeout)
				started := time.Now()
				result := e.Tools.Execute(toolCtx, pending.Call)
				cancel()
				pending.Result, pending.State = &result, "completed"
				run.Messages = append(run.Messages, Message{Role: "tool", CallID: pending.Call.ID, Content: result.JSON()})
				if err := e.save(run, "tool_finished"); err != nil {
					return err
				}
				if result.Code == "uncertain" {
					return e.stop(run, "blocked", ErrUncertain)
				}
				if e.Emit != nil {
					e.Emit(Event{RunID: run.ID, Kind: "tool_observed", CallID: pending.Call.ID, Tool: pending.Call.Name, Code: result.Code, Duration: time.Since(started)})
				}
			}
			run.Pending, run.Phase = nil, "model"
			if err := e.save(run, "ready_for_model"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported run phase: %s", run.Phase)
		}
	}
}

func validateReply(run *Run, reply Message) error {
	if reply.Role != "assistant" {
		return errors.New("invalid model role")
	}
	if len(reply.Content) > 128*1024 || len(reply.Calls) > 32 {
		return errors.New("model output exceeds limits")
	}
	if len(reply.Calls) == 0 && reply.Content == "" {
		return errors.New("empty model answer")
	}
	seen := map[string]bool{}
	for _, message := range run.Messages {
		for _, call := range message.Calls {
			seen[call.ID] = true
		}
	}
	for _, call := range reply.Calls {
		if call.ID == "" || len(call.ID) > 200 || call.Name == "" || seen[call.ID] {
			return errors.New("invalid or reused tool call ID")
		}
		if len(call.Arguments) > 32768 || !json.Valid(call.Arguments) {
			return errors.New("malformed tool arguments")
		}
		seen[call.ID] = true
	}
	return nil
}
