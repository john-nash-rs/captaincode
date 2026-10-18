package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/john-nash-rs/captaincode/core"
)

func completedRun(question, answer string) core.Run {
	return core.Run{
		ID:    "run-1",
		Phase: "completed",
		Messages: []core.Message{
			{Role: "system", Content: core.SystemPrompt},
			{Role: "user", Content: question},
			{Role: "assistant", Content: answer},
		},
	}
}

func userTexts(run core.Run) []string {
	var out []string
	for _, m := range run.Messages {
		if m.Role == "user" {
			out = append(out, m.Content)
		}
	}
	return out
}

// THE BUG. Every prompt started a fresh run holding only the system prompt and
// that one question, so "save it in a file" arrived with nothing to refer to
// and the model said it had no idea what "it" meant.
func TestSecondPromptSeesTheFirst(t *testing.T) {
	s := &session{}
	if _, err := s.next("give me hello world api in flask", nil); err != nil {
		t.Fatal(err)
	}
	s.record(completedRun("give me hello world api in flask", "```python\nfrom flask import Flask\n```"))

	second, err := s.next("save it in a file", nil)
	if err != nil {
		t.Fatal(err)
	}
	texts := userTexts(second)
	if len(texts) != 2 {
		t.Fatalf("the follow-up sees %d user messages, want 2: %v", len(texts), texts)
	}
	if texts[0] != "give me hello world api in flask" {
		t.Errorf("the earlier question is missing: %v", texts)
	}
	if texts[1] != "save it in a file" {
		t.Errorf("the follow-up is wrong: %v", texts)
	}

	// The assistant's earlier answer must be present too, or the follow-up can
	// see the question but not what was said in reply.
	var sawAnswer bool
	for _, m := range second.Messages {
		if strings.Contains(m.Content, "from flask import Flask") {
			sawAnswer = true
		}
	}
	if !sawAnswer {
		t.Error("the earlier answer is missing from the follow-up context")
	}
}

// Before anything has run, the first prompt starts a normal run.
func TestFirstPromptStartsAFreshRun(t *testing.T) {
	s := &session{}
	run, err := s.next("hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase != "model" {
		t.Fatalf("phase = %q, want model", run.Phase)
	}
	if got := userTexts(run); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("messages = %v, want just the question", got)
	}
	if run.Messages[0].Role != "system" {
		t.Fatal("the system prompt is missing")
	}
}

// A failed run may have applied some of its tool calls. Continuing from it
// would let the model refer to work no observation confirms, so the
// conversation restarts instead and says why.
func TestFailedRunIsNotContinued(t *testing.T) {
	s := &session{}
	var reason error
	s.onNew = func(err error) { reason = err }
	s.previous = &core.Run{
		ID: "run-1", Phase: "failed",
		Messages: []core.Message{
			{Role: "system", Content: core.SystemPrompt},
			{Role: "user", Content: "first"},
		},
	}

	run, err := s.next("second", nil)
	if err != nil {
		t.Fatalf("a failed conversation should not stop the prompt: %v", err)
	}
	if got := userTexts(run); len(got) != 1 || got[0] != "second" {
		t.Fatalf("messages = %v, want only the new question", got)
	}
	if reason == nil {
		t.Error("the reason for restarting was not reported")
	}
	if s.previous != nil {
		t.Error("the failed run is still being carried forward")
	}
}

// record must refuse a run that did not complete.
func TestOnlyCompletedRunsAreRecorded(t *testing.T) {
	for _, phase := range []string{"failed", "cancelled", "blocked", "tools"} {
		s := &session{}
		s.record(core.Run{ID: "x", Phase: phase})
		if s.previous != nil {
			t.Errorf("a %s run was recorded", phase)
		}
	}
	s := &session{}
	s.record(core.Run{ID: "x", Phase: "completed"})
	if s.previous == nil {
		t.Error("a completed run was not recorded")
	}
}

// The system prompt leads once. Carrying it forward on every turn would grow
// the conversation by a fixed amount per prompt for no benefit.
func TestSystemPromptIsNotDuplicated(t *testing.T) {
	s := &session{}
	for range 5 {
		run, err := s.next("question", nil)
		if err != nil {
			t.Fatal(err)
		}
		// A run leaves next() in flight; only a finished one is carried on.
		run.Phase = "completed"
		run.Messages = append(run.Messages, core.Message{Role: "assistant", Content: "ok"})
		s.record(run)
	}
	if s.previous == nil {
		t.Fatal("nothing was recorded")
	}
	systemCount := 0
	for _, m := range s.previous.Messages {
		if m.Role == "system" {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Fatalf("the conversation holds %d system prompts, want 1", systemCount)
	}
	// Five questions must all still be present.
	if got := len(userTexts(*s.previous)); got != 5 {
		t.Fatalf("the conversation holds %d questions, want 5", got)
	}
}

// A follow-up resumes a named run rather than continuing the current one.
func TestResumeOverridesTheConversation(t *testing.T) {
	s := &session{resume: "abc123"}
	earlier := completedRun("earlier", "answer")
	s.previous = &earlier

	loaded := completedRun("resumed", "…")
	loaded.ID = "resumed"
	run, err := s.next("next", func(_ context.Context, id string) (core.Run, error) {
		if id != "abc123" {
			t.Errorf("loaded %q, want abc123", id)
		}
		return loaded, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "resumed" {
		t.Fatalf("run = %q, want the resumed one", run.ID)
	}
	if s.resume != "" {
		t.Error("the resume id was not cleared, so it would apply twice")
	}
}

// A resume that cannot be loaded is an error, not a silent fresh start.
func TestResumeFailureIsReported(t *testing.T) {
	s := &session{resume: "gone"}
	_, err := s.next("next", func(context.Context, string) (core.Run, error) {
		return core.Run{}, errors.New("no such run")
	})
	if err == nil {
		t.Fatal("a failed resume was reported as success")
	}
}

// The interface shows how many prompts the conversation holds, so somebody can
// see that a follow-up joined an existing conversation.
func TestTurnsCountsUserPrompts(t *testing.T) {
	s := &session{}
	if s.turns() != 0 {
		t.Fatalf("turns = %d before any prompt", s.turns())
	}
	run := completedRun("one", "answer")
	s.record(run)
	if s.turns() != 1 {
		t.Fatalf("turns = %d, want 1", s.turns())
	}
	run = core.Run{ID: run.ID, Phase: "completed", Messages: append(run.Messages,
		core.Message{Role: "user", Content: "two"},
		core.Message{Role: "assistant", Content: "ok"})}
	s.record(run)
	if s.turns() != 2 {
		t.Fatalf("turns = %d, want 2", s.turns())
	}
}
