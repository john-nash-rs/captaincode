package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type memory struct {
	run    Run
	events []string
	fail   string
}

func (m *memory) Save(_ context.Context, run Run, event string) error {
	if event == m.fail {
		return errors.New("simulated disk failure")
	}
	data, _ := json.Marshal(run)
	json.Unmarshal(data, &m.run)
	m.events = append(m.events, event)
	return nil
}

type fakeTools struct {
	executed int
	result   Result
	before   func()
}

func (f *fakeTools) Definitions() []Definition   { return nil }
func (f *fakeTools) ReplaySafe(name string) bool { return name == "read_file" }
func (f *fakeTools) Execute(context.Context, Call) Result {
	if f.before != nil {
		f.before()
	}
	f.executed++
	return f.result
}

type modelFunc func(context.Context, []Message, []Definition) (Message, error)

func (f modelFunc) Complete(ctx context.Context, h []Message, d []Definition) (Message, error) {
	return f(ctx, h, d)
}
func engine(model Model, tools Executor, store Store) *Engine {
	return &Engine{Model: model, Tools: tools, Store: store, MaxTurns: 4, MaxToolCalls: 4, MaxContextBytes: 65536, ToolTimeout: time.Second}
}

func TestIntentAndResultAreDurableAtTheirBoundaries(t *testing.T) {
	store := &memory{}
	tools := &fakeTools{result: Result{OK: true, Text: `{"version":"2.5.0"}`}}
	tools.before = func() {
		if store.run.Phase != "tools" || store.run.Pending[0].State != "running" {
			t.Fatal("effect began before durable intent")
		}
	}
	run := NewRun("one", "version?")
	err := engine(Fixture{}, tools, store).Run(context.Background(), &run)
	if err != nil || run.Phase != "completed" || tools.executed != 1 || len(run.Messages) != 5 {
		t.Fatal(run, err)
	}
	if store.run.Messages[3].CallID != store.run.Messages[2].Calls[0].ID {
		t.Fatal("result lost correlation")
	}
}

func TestDiskFailureBeforeExecutionPreventsEffect(t *testing.T) {
	store := &memory{fail: "tool_started"}
	tools := &fakeTools{}
	run := NewRun("disk", "version?")
	if err := engine(Fixture{}, tools, store).Run(context.Background(), &run); err == nil || tools.executed != 0 {
		t.Fatal("ran without durable intent")
	}
}

func TestCrashAfterEffectDoesNotReplayMutation(t *testing.T) {
	store := &memory{fail: "tool_finished"}
	tools := &fakeTools{result: Result{OK: true, Text: "written"}}
	model := modelFunc(func(context.Context, []Message, []Definition) (Message, error) {
		return Message{Role: "assistant", Calls: []Call{{ID: "write-1", Name: "write_file", Arguments: json.RawMessage(`{}`)}}}, nil
	})
	run := NewRun("crash", "write")
	first := engine(model, tools, store)
	if err := first.Run(context.Background(), &run); err == nil || tools.executed != 1 {
		t.Fatal("failure injection did not hit uncertain window")
	}
	store.fail = ""
	recovered := store.run
	if err := first.Recover(&recovered); !errors.Is(err, ErrUncertain) || recovered.Phase != "blocked" {
		t.Fatal("uncertain mutation was not blocked", err)
	}
	if tools.executed != 1 {
		t.Fatal("recovery repeated mutation")
	}
}

func TestCompletedResultsSurviveRecovery(t *testing.T) {
	store := &memory{}
	tools := &fakeTools{result: Result{OK: true, Text: `{"version":"2.5.0"}`}}
	run := NewRun("saved", "version?")
	call := Call{ID: "saved-call", Name: "read_file", Arguments: json.RawMessage(`{"path":"package.json"}`)}
	run.Messages = append(run.Messages, Message{Role: "assistant", Calls: []Call{call}}, Message{Role: "tool", CallID: call.ID, Content: tools.result.JSON()})
	run.Phase = "tools"
	run.Pending = []Pending{{Call: call, State: "completed", Result: &tools.result}}
	e := engine(Fixture{}, tools, store)
	if err := e.Recover(&run); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), &run); err != nil || tools.executed != 0 {
		t.Fatal("saved result was repeated", err)
	}
}

func TestCancelledRunCannotResume(t *testing.T) {
	store := &memory{}
	tools := &fakeTools{}
	run := NewRun("cancel", "version?")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := engine(Fixture{}, tools, store)
	if err := e.Run(ctx, &run); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), &run); err == nil || tools.executed != 0 {
		t.Fatal("cancelled run resumed")
	}
}

func TestBudgetsAndInvalidBatchPreventCalls(t *testing.T) {
	for _, calls := range [][]Call{
		{{ID: "a", Name: "read_file", Arguments: json.RawMessage(`{}`)}, {ID: "b", Name: "read_file", Arguments: json.RawMessage(`{}`)}},
		{{ID: "same", Name: "read_file", Arguments: json.RawMessage(`{}`)}, {ID: "same", Name: "read_file", Arguments: json.RawMessage(`{}`)}},
	} {
		tools := &fakeTools{}
		run := NewRun("budget", "read")
		e := engine(modelFunc(func(context.Context, []Message, []Definition) (Message, error) {
			return Message{Role: "assistant", Calls: calls}, nil
		}), tools, &memory{})
		e.MaxToolCalls = 1
		if err := e.Run(context.Background(), &run); err == nil || tools.executed != 0 {
			t.Fatal("invalid batch executed")
		}
	}
}

func TestContextKeepsRequestResultPairs(t *testing.T) {
	history := []Message{{Role: "system", Content: "Do not modify files"}, {Role: "user", Content: "Inspect services/api"}}
	for index := 0; index < 3; index++ {
		id := fmt.Sprint(index)
		history = append(history, Message{Role: "assistant", Calls: []Call{{ID: id, Name: "read_file", Arguments: json.RawMessage(`{}`)}}}, Message{Role: "tool", CallID: id, Content: strings.Repeat("x", 300)})
	}
	selected, err := SelectContext(history, 900)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) >= len(history) || selected[0].Content != history[0].Content || selected[1].Content != history[1].Content {
		t.Fatal("lost constraints or failed to prune")
	}
	for index, message := range selected {
		if message.Role == "tool" && selected[index-1].Calls[0].ID != message.CallID {
			t.Fatal("orphaned result")
		}
	}
}

func TestSavedUncertaintyCannotBecomeSuccessAfterCrash(t *testing.T) {
	store := &memory{fail: "blocked"}
	tools := &fakeTools{result: Failure("uncertain", "reply lost")}
	model := modelFunc(func(context.Context, []Message, []Definition) (Message, error) {
		return Message{Role: "assistant", Calls: []Call{{ID: "mutation", Name: "write_file", Arguments: json.RawMessage(`{}`)}}}, nil
	})
	run := NewRun("uncertain", "write")
	e := engine(model, tools, store)
	if err := e.Run(context.Background(), &run); err == nil {
		t.Fatal("uncertainty not reported")
	}
	if store.run.Phase != "tools" || store.run.Pending[0].Result.Code != "uncertain" {
		t.Fatal("did not reach requested crash boundary")
	}
	store.fail = ""
	recovered := store.run
	if err := e.Recover(&recovered); !errors.Is(err, ErrUncertain) || recovered.Phase != "blocked" {
		t.Fatal("saved uncertainty escaped recovery", err)
	}
	if tools.executed != 1 {
		t.Fatal("mutation repeated")
	}
}
