package core

import (
	"context"
	"encoding/json"
	"fmt"
)

// Fixture solves one teaching task; it is not a language model or general agent.
type Fixture struct{}

func (Fixture) Complete(ctx context.Context, history []Message, _ []Definition) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	last := history[len(history)-1]
	if last.Role == "tool" {
		var result Result
		if err := json.Unmarshal([]byte(last.Content), &result); err != nil {
			return Message{}, err
		}
		if !result.OK {
			return Message{Role: "assistant", Content: "I could not determine the version: " + result.Code}, nil
		}
		var document struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(result.Text), &document); err != nil || document.Version == "" {
			return Message{Role: "assistant", Content: "The file did not contain a usable version field."}, nil
		}
		return Message{Role: "assistant", Content: fmt.Sprintf("The project version is %s (package.json).", document.Version)}, nil
	}
	return Message{Role: "assistant", Calls: []Call{{ID: "fixture-read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"package.json"}`)}}}, nil
}
