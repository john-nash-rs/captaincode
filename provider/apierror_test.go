package provider

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The old failure text was identical for a missing model, a refused key and a
// rate limit, which left no way to tell them apart or to act. Every case a user
// can fix must now say what to do.
func TestAPIErrorsCarryTheirCauseAndARemedy(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantIn     string
		wantAdvice string
	}{
		{
			name:       "invalid model",
			body:       `{"error":{"code":400,"message":"no-such/model is not a valid model ID"}}`,
			wantIn:     "not a valid model",
			wantAdvice: "does not exist",
		},
		{
			name:       "rejected key",
			body:       `{"error":{"code":401,"message":"No auth credentials found"}}`,
			wantIn:     "No auth credentials",
			wantAdvice: "/openrouter api-key",
		},
		{
			name:       "no credit",
			body:       `{"error":{"code":402,"message":"Insufficient credits"}}`,
			wantIn:     "Insufficient credits",
			wantAdvice: "credit",
		},
		{
			name:       "rate limited",
			body:       `{"error":{"code":429,"message":"Rate limit exceeded"}}`,
			wantIn:     "Rate limit",
			wantAdvice: "Wait",
		},
		{
			name:       "gated free model",
			body:       `{"error":{"code":403,"message":"thinkingmachines/inkling:free is only available on agentic harnesses."}}`,
			wantIn:     "agentic harnesses",
			wantAdvice: "restricted",
		},
		{
			name:       "no endpoints",
			body:       `{"error":{"code":400,"message":"No endpoints found for z-ai/glm-5.3"}}`,
			wantIn:     "No endpoints",
			wantAdvice: "/models",
		},
		{
			name:       "context too long",
			body:       `{"error":{"code":400,"message":"This model's maximum context length is 8192 tokens"}}`,
			wantIn:     "maximum context length",
			wantAdvice: "larger context",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := parseAPIError("a/b", errors.New(tc.body))
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("message = %q, want it to mention %q", err.Error(), tc.wantIn)
			}
			if advice := err.Advice(); !strings.Contains(advice, tc.wantAdvice) {
				t.Errorf("advice = %q, want it to mention %q", advice, tc.wantAdvice)
			}
		})
	}
}

// Every fixable failure must tell the user to pick another model. Being told
// "something failed" with no next step was the whole problem.
func TestAdviceAlwaysSuggestsAnAlternativeWhenTheModelIsAtFault(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":400,"message":"no-such/model is not a valid model ID"}}`,
		`{"error":{"code":403,"message":"x is only available on agentic harnesses."}}`,
		`{"error":{"code":400,"message":"No endpoints found for x"}}`,
		`{"error":{"code":402,"message":"Insufficient credits"}}`,
	} {
		advice := parseAPIError("x", errors.New(body)).Advice()
		if advice == "" {
			t.Errorf("%s produced no advice", body)
		}
	}
}

// An unrecognised failure must not invent a cause. The provider's own text is
// shown, which is honest and often useful, but no remedy is invented: a wrong
// remedy is worse than none, because it sends someone down the wrong path.
func TestUnrecognisedFailureIsHonest(t *testing.T) {
	err := parseAPIError("x", errors.New("something entirely unexpected"))
	if !strings.Contains(err.Error(), "something entirely unexpected") {
		t.Errorf("message = %q, want the provider text passed through", err.Error())
	}
	if advice := err.Advice(); advice != "" {
		t.Errorf("an unrecognised failure produced invented advice: %q", advice)
	}

	// A failure with no text at all must still produce something printable,
	// and must admit that the cause is unknown rather than staying blank.
	empty := parseAPIError("x", errors.New(""))
	if empty.Error() == "" {
		t.Fatal("an empty failure produced an empty message")
	}
	if !strings.Contains(empty.Error(), "unrecognised") {
		t.Errorf("message = %q, want it to admit the cause is unknown", empty.Error())
	}
}

// A body that is not the expected envelope must still yield a usable message,
// not a nil dereference or an empty string.
func TestUnparseableBodyStillProducesAMessage(t *testing.T) {
	err := parseAPIError("x", errors.New("connection reset by peer"))
	if err.Error() == "" {
		t.Fatal("an unparseable body produced an empty message")
	}
}

// A status code must be recovered without matching inside a longer number.
func TestStatusMatchingIgnoresSubstrings(t *testing.T) {
	if containsToken("error 1400 occurred", 400) {
		t.Error("1400 was read as 400")
	}
	if !containsToken("error 400 occurred", 400) {
		t.Error("400 was not recognised")
	}
	if !containsToken("status-code: 429", 429) {
		t.Error("429 with punctuation was not recognised")
	}
}

// A very long body must not be dumped into the transcript.
func TestLongBodyIsTruncated(t *testing.T) {
	err := parseAPIError("x", fmt.Errorf("code %s", strings.Repeat("a", 5000)))
	if len(err.Error()) > 400 {
		t.Fatalf("message is %d characters", len(err.Error()))
	}
}

// adviceFor must find the remedy through a wrapped error, since the engine
// wraps provider failures with the run identifier before the interface sees it.
func TestAdviceSurvivesWrapping(t *testing.T) {
	inner := parseAPIError("x", errors.New(`{"error":{"code":400,"message":"a/b is not a valid model ID"}}`))
	wrapped := fmt.Errorf("run abc123: %w", inner)
	if !strings.Contains(adviceFor(wrapped), "/models") {
		t.Fatalf("advice was lost through wrapping: %q", adviceFor(wrapped))
	}
	if adviceFor(nil) != "" {
		t.Error("a nil error produced advice")
	}
	if adviceFor(errors.New("plain")) != "" {
		t.Error("a plain error produced advice")
	}
}
