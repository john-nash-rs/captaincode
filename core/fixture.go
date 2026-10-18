package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Fixture solves a small set of scripted tasks offline. It is not a language
// model and does not pretend to be one.
//
// Two tasks exist because a harness cannot be exercised without one that both
// reads and writes, and every provider other than this one needs a network and
// a paid account. A test that needs a credential is a test that does not run in
// CI, which is the same as no test.
type Fixture struct{}

func (Fixture) Complete(ctx context.Context, history []Message, definitions []Definition) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	last := history[len(history)-1]
	if last.Role == "tool" {
		return Fixture{}.reported(history)
	}
	// Whether the request is a create request, and whether writing is
	// available, are separate questions: the first decides whether to engage,
	// the second decides whether it can be carried out.
	file, asked := createRequest(last.Content)
	switch {
	case asked && offers(definitions, "write_file"):
		return Message{Role: "assistant", Calls: []Call{file}}, nil
	case asked:
		// Saying so is better than quietly falling back to the read task,
		// which would look like the request was ignored.
		return Message{Role: "assistant", Content: "I cannot create files: writing is not enabled. Run /permissions writes on."}, nil
	}
	return Message{Role: "assistant", Calls: []Call{{ID: "fixture-read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"package.json"}`)}}}, nil
}

func offers(definitions []Definition, name string) bool {
	for _, d := range definitions {
		if d.Name == name {
			return true
		}
	}
	return false
}

// reported turns a tool observation into an answer.
func (Fixture) reported(history []Message) (Message, error) {
	var result Result
	if err := json.Unmarshal([]byte(history[len(history)-1].Content), &result); err != nil {
		return Message{}, err
	}
	if !result.OK {
		return Message{Role: "assistant", Content: "That did not work: " + result.Code}, nil
	}
	if strings.HasSuffix(result.Text, " package.json") || strings.Contains(result.Text, "version") {
		var document struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(result.Text), &document); err != nil || document.Version == "" {
			return Message{Role: "assistant", Content: "The file did not contain a usable version field."}, nil
		}
		return Message{Role: "assistant", Content: fmt.Sprintf("The project version is %s (package.json).", document.Version)}, nil
	}
	return Message{Role: "assistant", Content: "Done: " + result.Text}, nil
}

// createRequest recognises "create <file> containing <content>" and turns it
// into the single tool call that does it. Whether that call can be made is the
// caller's business, so it can distinguish "not that kind of request" from
// "that kind, but writing is off".
var createPattern = regexp.MustCompile(`(?is)create\s+(?:a\s+)?(?:file\s+)?([\w./-]+\.\w+)(?:\s+(?:containing|with|that says|holding)\s+(.+?))?\.?\s*$`)

func createRequest(prompt string) (Call, bool) {
	match := createPattern.FindStringSubmatch(prompt)
	if match == nil {
		return Call{}, false
	}
	content := strings.TrimSpace(match[2])
	if content == "" {
		content = "created by the fixture\n"
	}
	arguments, err := json.Marshal(map[string]any{
		"path":            match[1],
		"content":         content,
		"expected_sha256": "absent",
	})
	if err != nil {
		return Call{}, false
	}
	return Call{ID: "fixture-write-1", Name: "write_file", Arguments: arguments}, true
}
