package provider

import (
	"context"
	"errors"
	"testing"
)

// A model identifier that cannot work is caught without a network round trip,
// which is both faster and kinder than a 404.
func TestCheckRejectsAMalformedModelWithoutARequest(t *testing.T) {
	p := &OpenRouter{}
	for _, name := range []string{"", "no-slash", "/leading", "trailing/"} {
		got := p.Check(context.Background(), []string{name})
		if len(got) != 1 {
			t.Fatalf("%q produced %d results", name, len(got))
		}
		if !errors.Is(got[0].Err, ErrUnknownModel) {
			t.Errorf("%q reported %v, want ErrUnknownModel", name, got[0].Err)
		}
	}
}

// The whole value of the check is telling a bad key apart from a bad model,
// since those have completely different fixes.
func TestClassifySeparatesTheFailuresThatDiffer(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"401", errors.New("status 401 Unauthorized"), ErrBadCredential},
		{"invalid key", errors.New(`{"error":{"code":401,"message":"Invalid API key"}}`), ErrBadCredential},
		{"no credentials", errors.New("no auth credentials found"), ErrBadCredential},
		{"404", errors.New("404 model not found"), ErrUnknownModel},
		{"unavailable", errors.New("this model is not available"), ErrUnknownModel},
		{"dns", errors.New("dial tcp: lookup failed"), ErrUnreachable},
		{"timeout", context.DeadlineExceeded, ErrUnreachable},
		{"canceled", context.Canceled, ErrUnreachable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.err); !errors.Is(got, tc.want) {
				t.Errorf("classify(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// An error that matches nothing must fall back to unreachable rather than
// guessing. Guessing wrong here would tell someone their key is bad when it
// is not.
func TestClassifyFallsBackToUnreachable(t *testing.T) {
	if got := classify(errors.New("something entirely unexpected")); !errors.Is(got, ErrUnreachable) {
		t.Fatalf("classify = %v, want ErrUnreachable", got)
	}
}

// No models means nothing to ask about, which must be reported plainly rather
// than as a successful check of zero things.
func TestCheckWithNoModelsIsHonest(t *testing.T) {
	p := &OpenRouter{}
	if got := p.Check(context.Background(), nil); len(got) != 0 {
		t.Fatalf("got %v, want nothing to check", got)
	}
	summary, ok := Summarize(nil)
	if ok {
		t.Error("an empty check reported success")
	}
	if summary == "" {
		t.Error("an empty check produced no summary")
	}
}

func TestSummarizeReportsTheFirstWorkingModel(t *testing.T) {
	summary, ok := Summarize([]ProbeResult{
		{Model: "a", Err: ErrUnknownModel},
		{Model: "b"},
	})
	if !ok {
		t.Fatal("a working model was not recognised")
	}
	if summary == "" {
		t.Fatal("no summary for a working model")
	}
}

// A provider with no client cannot check anything, and must say so rather than
// panicking. This is reachable in practice: the interface holds a probe while
// the credential is still being entered.
func TestCheckWithoutAClientDoesNotPanic(t *testing.T) {
	p := &OpenRouter{}
	got := p.Check(context.Background(), []string{"openai/gpt-4o"})
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	if !errors.Is(got[0].Err, ErrNoCredential) {
		t.Fatalf("got %v, want ErrNoCredential", got[0].Err)
	}
}

// A cancelled context must return rather than block.
func TestCheckHonoursACancelledContext(t *testing.T) {
	p := &OpenRouter{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := p.Check(ctx, []string{"a/b"}); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}
