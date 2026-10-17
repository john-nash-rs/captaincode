// Package loop is the complete, deliberately small chapter 2 checkpoint.
package loop

import (
	"context"
	"errors"
	"fmt"
)

type Call struct {
	ID, Name, Arguments string
}

type Message struct {
	Role, Content, CallID string
	Calls                 []Call
}

type Model interface {
	Complete(context.Context, []Message) (Message, error)
}

type Execute func(context.Context, Call) (string, error)

var ErrBudget = errors.New("model turn budget exhausted")

// Run keeps a complete transcript and returns it even when execution stops.
// This teaching checkpoint is serial and has no persistence or OS sandbox.
func Run(
	ctx context.Context,
	model Model,
	execute Execute,
	question string,
	maxTurns int,
) ([]Message, error) {
	if maxTurns < 1 {
		return nil, errors.New("maxTurns must be positive")
	}
	history := []Message{
		{Role: "user", Content: question},
	}
	seen := map[string]bool{}
	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return history, err
		}
		request := append([]Message(nil), history...)
		reply, err := model.Complete(ctx, request)
		if err != nil {
			return history, err
		}
		if reply.Role != "assistant" {
			return history, errors.New(
				"model returned a non-assistant message",
			)
		}
		// Validate the entire batch before executing any of it.
		batch := map[string]bool{}
		for _, call := range reply.Calls {
			if call.ID == "" || call.Name == "" ||
				seen[call.ID] || batch[call.ID] {
				return history, errors.New(
					"invalid or reused tool-call identifier",
				)
			}
			batch[call.ID] = true
		}
		history = append(history, reply)
		if len(reply.Calls) == 0 {
			if reply.Content == "" {
				return history, errors.New("empty final answer")
			}
			return history, nil
		}
		for _, call := range reply.Calls {
			seen[call.ID] = true
			if err := ctx.Err(); err != nil {
				return history, err
			}
			result, err := execute(ctx, call)
			if err != nil {
				result = fmt.Sprintf("tool error: %s", err)
			}
			history = append(history, Message{
				Role:    "tool",
				CallID:  call.ID,
				Content: result,
			})
		}
	}
	return history, ErrBudget
}
