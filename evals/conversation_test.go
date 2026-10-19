package evals

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/storage"
	"github.com/john-nash-rs/captaincode/toolbox"
)

// FOUR PROMPTS, ONE CONVERSATION, OFFLINE.
//
// The binary cannot reach this. One-shot mode takes a single --question, and
// the interactive path needs a terminal, so the second prompt of a session was
// never exercised without a credential. A defect in the built-in model — every
// turn minting the same tool call ID — therefore went unnoticed until a person
// typed two prompts in a row: the engine rejects a reply that reuses an ID
// already on the record, so a repeated task failed with "invalid or reused tool
// call ID" while a different one appeared to work.
//
// The prompts repeat each kind of task on purpose. A read followed by a write
// draws two different IDs and hides the defect; two reads, or two writes, is
// what exposes it.
//
// The real engine, the real tools, and the real store are used rather than
// doubles, so this is the conversation a user actually gets.
func TestConversationStaysUsablePromptAfterPrompt(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "package.json"), []byte(`{"name":"demo","version":"2.5.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(t.TempDir(), "turns.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	engine := &core.Engine{
		Model:           core.Fixture{},
		Tools:           toolbox.Registry{Root: work, MaxBytes: 32768, AllowWrites: true},
		Store:           store,
		MaxTurns:        4,
		MaxToolCalls:    4,
		MaxContextBytes: 65536,
		ToolTimeout:     10 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prompts := []string{
		"what is the project version?",
		"what is the project version?",
		"create notes.txt containing hello from the second turn",
		"create other.txt containing a second file",
	}
	var conversation core.Run
	for index, prompt := range prompts {
		run := core.NewRun("turn-one", prompt)
		if index > 0 {
			run, err = core.FollowUp(conversation, prompt)
			if err != nil {
				t.Fatalf("prompt %d could not continue the conversation: %v", index+1, err)
			}
		}
		if err := engine.Run(ctx, &run); err != nil {
			t.Fatalf("prompt %d failed: %v", index+1, err)
		}
		if index < 2 {
			answer := run.Messages[len(run.Messages)-1].Content
			if !strings.Contains(answer, "2.5.0") {
				t.Fatalf("prompt %d did not answer the question: %q", index+1, answer)
			}
		}
		conversation = run
	}

	// The last prompt must still carry the first, or a follow-up referring to
	// earlier work has nothing to refer to.
	carried := false
	for _, message := range conversation.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "project version") {
			carried = true
		}
	}
	if !carried {
		t.Fatal("a later prompt did not see the first")
	}

	for name, want := range map[string]string{
		"notes.txt": "hello from the second turn",
		"other.txt": "a second file",
	} {
		data, err := os.ReadFile(filepath.Join(work, name))
		if err != nil {
			t.Fatalf("%s was not created: %v", name, err)
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("%s has the wrong contents: %q", name, data)
		}
	}
}
