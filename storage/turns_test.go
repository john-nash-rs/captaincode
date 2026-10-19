package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// TestTurnsPageBackwards checks the contract the interface pages with: a skip
// counts back from the newest exchange, a page comes out oldest-first so it can
// be prepended, and more says when to stop.
func TestTurnsPageBackwards(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for i, turn := range []Turn{
		{Question: "one", Answer: "a1"},
		{Question: "two", Answer: "a2"},
		{Question: "three", Answer: "a3"},
		{Question: "four", Answer: "a4"},
		{Question: "five", Answer: "a5"},
	} {
		if err := store.AppendTurn(ctx, "run", i+1, turn.Question, turn.Answer); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		skip     int
		limit    int
		first    string
		last     string
		wantMore bool
	}{
		{skip: 0, limit: 2, first: "four", last: "five", wantMore: true},
		{skip: 2, limit: 2, first: "two", last: "three", wantMore: true},
		{skip: 4, limit: 2, first: "one", last: "one", wantMore: false},
		{skip: 0, limit: 10, first: "one", last: "five", wantMore: false},
	}
	for _, tc := range cases {
		page, more, err := store.Turns(ctx, "run", tc.skip, tc.limit)
		if err != nil {
			t.Fatal(err)
		}
		if more != tc.wantMore {
			t.Errorf("skip=%d limit=%d more=%v want %v", tc.skip, tc.limit, more, tc.wantMore)
		}
		if len(page) == 0 || page[0].Question != tc.first || page[len(page)-1].Question != tc.last {
			t.Errorf("skip=%d limit=%d page=%v want %s..%s", tc.skip, tc.limit, page, tc.first, tc.last)
		}
	}

	// A turn is identified by its position, so a retry replaces rather than
	// duplicates it. Reading the conversation back whole is how that shows:
	// five turns, the last carrying the corrected answer.
	if err := store.AppendTurn(ctx, "run", 5, "five", "corrected"); err != nil {
		t.Fatal(err)
	}
	page, more, err := store.Turns(ctx, "run", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 5 || page[4].Question != "five" || page[4].Answer != "corrected" {
		t.Fatalf("after the rewrite the conversation is %v (more=%v), want five turns ending corrected", page, more)
	}

	// Conversations are keyed apart: one run's turns must not appear in another.
	if page, _, err := store.Turns(ctx, "other", 0, 10); err != nil || len(page) != 0 {
		t.Fatalf("another conversation saw turns: %v %v", page, err)
	}
}
