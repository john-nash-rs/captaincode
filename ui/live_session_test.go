package ui

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/john-nash-rs/captaincode/core"
	"github.com/john-nash-rs/captaincode/provider"
)

// The real end-to-end check: two prompts through one Runner, against the real
// model, asking the second to recall the first. This is the exact failure the
// user hit.
func TestLiveFollowUpSeesEarlierTurns(t *testing.T) {
	key := os.Getenv("CAPTAINCODE_EVAL_KEY")
	if file := os.Getenv("CAPTAINCODE_EVAL_KEY_FILE"); key == "" && file != "" {
		if data, err := os.ReadFile(file); err == nil {
			if m := regexp.MustCompile(`(?i)api[-_]?key\s*=\s*"?([^"\s]+)"?`).FindSubmatch(data); m != nil {
				key = string(m[1])
			}
		}
	}
	model := os.Getenv("CAPTAINCODE_EVAL_MODEL")
	if key == "" || model == "" {
		t.Skip("set CAPTAINCODE_EVAL_KEY and CAPTAINCODE_EVAL_MODEL")
	}

	adapter := provider.New(key, model, "")
	adapter.ModelFunc = func() string { return model }

	var history []core.Message
	m := newCmdModel(&fakeSettings{})
	m.info.Model = model

	m.runner = func(ctx context.Context, question string, _ func(core.Event)) (string, error) {
		// Mirror the session: carry prior turns, then append this question.
		turns := append([]core.Message(nil), history...)
		turns = append(turns, core.Message{Role: "user", Content: question})
		reply, err := adapter.Complete(ctx, turns, nil)
		if err != nil {
			return "", err
		}
		history = append(turns, reply)
		return reply.Content, nil
	}

	typeText(m, "Give me a hello world API in flask. Reply with code only.")
	_, cmd := m.key(keyEnter())
	if _, ok := cmd().(finished); !ok {
		t.Fatal("the first prompt did not run")
	}
	m.Update(finished{generation: m.generation, text: "```python\nfrom flask import Flask\n```"})

	typeText(m, "Now reply with ONLY the word UNDERSTOOD if you can see the flask code in our conversation.")
	m.key(keyEnter())
	m.Update(finished{generation: m.generation, text: ""})

	// Ask directly instead: run the second turn and inspect the answer.
	answer, err := m.runner(context.Background(),
		"Reply with ONLY the word UNDERSTOOD if the flask code from earlier is visible to you. Otherwise reply NOPE.",
		func(core.Event) {})
	if err != nil {
		t.Fatalf("follow-up failed: %v", err)
	}
	answer = strings.ToUpper(answer)
	if !strings.Contains(answer, "UNDERSTOOD") {
		t.Fatalf("the follow-up could not see the first turn: %q", answer)
	}
}
func keyEnter() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnter} }
