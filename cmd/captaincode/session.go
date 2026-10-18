package main

import (
	"context"
	"fmt"

	"github.com/john-nash-rs/captaincode/core"
)

// session carries the conversation between prompts.
//
// It exists as a type rather than as a closure variable so the behaviour can be
// tested directly: whether a second prompt sees the first is the whole point,
// and it was wrong for a long time without anything noticing.
type session struct {
	// previous is the last completed run, or nil before the first prompt.
	previous *core.Run
	// resume, when set, continues a run loaded from storage instead.
	resume string
	// onNew is called when a conversation cannot be continued, so the reason
	// reaches the operator instead of being silently swallowed.
	onNew func(error)
}

// next returns the run to execute for one prompt.
//
// A conversation continues only from a completed run. A run that failed or ran
// out of budget may have applied some of its tool calls, and continuing from
// there would let the model refer to work no observation confirms.
func (s *session) next(question string, load func(context.Context, string) (core.Run, error)) (core.Run, error) {
	if s.resume != "" {
		id := s.resume
		s.resume = ""
		loaded, err := load(context.Background(), id)
		if err != nil {
			return core.Run{}, err
		}
		return loaded, nil
	}
	if s.previous == nil {
		return core.NewRun(newRunID(), question), nil
	}
	continued, err := core.FollowUp(*s.previous, question)
	if err != nil {
		if s.onNew != nil {
			s.onNew(err)
		}
		s.previous = nil
		return core.NewRun(newRunID(), question), nil
	}
	return continued, nil
}

// record keeps a completed run so the next prompt can continue it.
func (s *session) record(run core.Run) {
	if run.Phase == "completed" {
		s.previous = &run
	}
}

// turns reports how many prompts the conversation holds. The interface shows
// it, so somebody can see that a follow-up joined an existing conversation
// rather than starting alone.
func (s *session) turns() int {
	if s.previous == nil {
		return 0
	}
	count := 0
	for _, m := range s.previous.Messages {
		if m.Role == "user" {
			count++
		}
	}
	return count
}

var _ = fmt.Sprintf
