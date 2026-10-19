package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/storage"
	"github.com/john-nash-rs/captaincode/ui"
)

// conversationID names the run the interface pages back through.
//
// It is not known until the first prompt resolves whether a conversation is
// being continued or started, but the interface needs it before that to know
// which history belongs on screen after --resume. It is written by the run
// goroutine and read by the interface goroutine, so it is guarded.
type conversationID struct {
	mu sync.RWMutex
	id string
}

func (c *conversationID) set(id string) {
	c.mu.Lock()
	c.id = id
	c.mu.Unlock()
}

func (c *conversationID) get() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.id
}

// history serves a conversation's earlier exchanges to the interface.
//
// The interface holds a window of the conversation and asks for what comes
// before it as the operator scrolls up, which is what makes the transcript
// unlimited without holding every answer on screen at once.
type history struct {
	db   *storage.DB
	conv *conversationID
}

func (h history) Older(skip, limit int) ([]ui.Turn, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	page, more, err := h.db.Turns(ctx, h.conv.get(), skip, limit)
	if err != nil {
		return nil, false, err
	}
	turns := make([]ui.Turn, len(page))
	for i, turn := range page {
		turns[i] = ui.Turn{Question: turn.Question, Answer: turn.Answer}
	}
	return turns, more, nil
}

// recordTurn keeps one exchange so the interface can page back to it.
//
// The run snapshot is overwritten each time the conversation grows, so it can
// only ever describe the newest state; without this there would be nothing
// behind the window to scroll to. Only a completed run is kept: a run that
// failed may have applied some of its tool calls, and showing it as a turn
// would present work that no observation confirms as though it had happened.
func recordTurn(db *storage.DB, run core.Run, question, answer string) {
	if db == nil || run.Phase != "completed" {
		return
	}
	ordinal := 0
	for _, message := range run.Messages {
		if message.Role == "user" {
			ordinal++
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.AppendTurn(ctx, run.ID, ordinal, question, answer); err != nil {
		fmt.Fprintf(os.Stderr, "could not record turn: %v\n", err)
	}
}
