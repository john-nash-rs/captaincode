package loop

import (
	"context"
	"errors"
	"testing"
)

type modelFunc func(context.Context, []Message) (Message, error)

func (f modelFunc) Complete(ctx context.Context, history []Message) (Message, error) {
	return f(ctx, history)
}

func TestResultReturnsToModelWithItsCallID(t *testing.T) {
	calls := 0
	model := modelFunc(func(_ context.Context, history []Message) (Message, error) {
		calls++
		if calls == 1 {
			return Message{Role: "assistant", Calls: []Call{{ID: "read-1", Name: "read_file", Arguments: `{"path":"package.json"}`}}}, nil
		}
		if len(history) != 3 || history[1].Calls[0].ID != "read-1" || history[2].CallID != "read-1" {
			t.Fatalf("broken transcript: %#v", history)
		}
		if history[2].Content != `{"version":"2.5.0"}` {
			t.Fatal("model did not receive tool output")
		}
		return Message{Role: "assistant", Content: "The version is 2.5.0."}, nil
	})
	history, err := Run(context.Background(), model, func(context.Context, Call) (string, error) {
		return `{"version":"2.5.0"}`, nil
	}, "What is the version?", 3)
	if err != nil || len(history) != 4 || calls != 2 {
		t.Fatalf("history=%#v calls=%d error=%v", history, calls, err)
	}
}

func TestToolFailureIsAnObservation(t *testing.T) {
	model := modelFunc(func(_ context.Context, history []Message) (Message, error) {
		if len(history) == 1 {
			return Message{Role: "assistant", Calls: []Call{{ID: "missing", Name: "read_file"}}}, nil
		}
		if history[2].Content != "tool error: file not found" {
			t.Fatal("failure was hidden")
		}
		return Message{Role: "assistant", Content: "I could not find that file."}, nil
	})
	_, err := Run(context.Background(), model, func(context.Context, Call) (string, error) {
		return "", errors.New("file not found")
	}, "Find the version", 2)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedCallCannotExecuteTwice(t *testing.T) {
	executed := 0
	model := modelFunc(func(context.Context, []Message) (Message, error) {
		return Message{Role: "assistant", Calls: []Call{{ID: "same", Name: "read_file"}}}, nil
	})
	_, err := Run(context.Background(), model, func(context.Context, Call) (string, error) {
		executed++
		return "ok", nil
	}, "Loop", 4)
	if err == nil || executed != 1 {
		t.Fatalf("executed=%d error=%v", executed, err)
	}
}

func TestBudgetAndCancellation(t *testing.T) {
	count := 0
	model := modelFunc(func(context.Context, []Message) (Message, error) {
		count++
		return Message{Role: "assistant", Calls: []Call{{ID: string(rune('a' + count)), Name: "read_file"}}}, nil
	})
	execute := func(context.Context, Call) (string, error) { return "ok", nil }
	_, err := Run(context.Background(), model, execute, "Loop", 2)
	if !errors.Is(err, ErrBudget) || count != 2 {
		t.Fatal("budget did not stop the loop")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Run(ctx, model, execute, "Cancelled", 2)
	if !errors.Is(err, context.Canceled) || count != 2 {
		t.Fatal("cancelled run made a model call")
	}
}
