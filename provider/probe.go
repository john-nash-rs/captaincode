package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ProbeResult is the outcome of checking one model identifier.
//
// It deliberately makes one request per model rather than one request overall.
// A credential that is rejected and a model that does not exist are different
// problems with different fixes, and a single combined check can only report
// "something is wrong", which is what made setting this up confusing.
type ProbeResult struct {
	Model string
	// Err is nil when the model answered. Otherwise it is one of the
	// sentinels below, so callers can tell the cases apart.
	Err error
}

var (
	// ErrNoCredential means no key was available to check with.
	ErrNoCredential = errors.New("no API key is configured")
	// ErrBadCredential means the key was rejected.
	ErrBadCredential = errors.New("the API key was rejected")
	// ErrUnknownModel means the key worked but the model does not exist.
	ErrUnknownModel = errors.New("no such model")
	// ErrUnreachable means the request never completed.
	ErrUnreachable = errors.New("could not reach the provider")
)

// Check verifies a credential and a model together with a single request per
// model, reporting the outcome without ever putting the credential at rest.
//
// The check is a GET for the model's metadata rather than a chat completion, so
// it costs no tokens and does not bill the account. It still authenticates, so
// a wrong key fails here rather than on the first prompt.
func (p *OpenRouter) Check(ctx context.Context, models []string) []ProbeResult {
	if len(models) == 0 {
		return nil
	}
	// A single generous ceiling for the whole check. Several models are probed
	// together, and one unreachable endpoint must not hang the interface.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	results := make([]ProbeResult, 0, len(models))
	for _, name := range models {
		results = append(results, p.checkOne(ctx, name))
	}
	return results
}

// checkOne authenticates, then asks the provider to describe one model.
func (p *OpenRouter) checkOne(ctx context.Context, name string) ProbeResult {
	name = strings.TrimSpace(name)
	author, slug, ok := strings.Cut(name, "/")
	if !ok || author == "" || slug == "" {
		// A model identifier is "author/name". Reporting this as a malformed
		// identifier rather than a missing model saves a round trip to a
		// 404 for someone who mistyped a slash.
		return ProbeResult{Model: name, Err: ErrUnknownModel}
	}
	// A provider built without a client cannot reach anything. Checking here
	// keeps a zero-value adapter from dereferencing nil, which would take the
	// whole interface down instead of reporting an unconfigured setup.
	if p == nil || p.Client == nil || p.Client.APIKeys == nil || p.Client.Models == nil {
		return ProbeResult{Model: name, Err: ErrNoCredential}
	}

	// Authenticate first, and separately.
	//
	// An earlier version checked the model endpoint alone, and that endpoint
	// turns out to serve metadata to unauthenticated callers: a live eval with
	// a deliberately invalid key reported "connected". The check was therefore
	// unable to detect the single most common setup mistake. /auth/key does
	// authenticate, and costs nothing.
	if _, err := p.Client.APIKeys.GetCurrentKeyMetadata(ctx); err != nil {
		return ProbeResult{Model: name, Err: classify(err)}
	}
	if _, err := p.Client.Models.Get(ctx, author, slug); err != nil {
		return ProbeResult{Model: name, Err: classify(err)}
	}
	return ProbeResult{Model: name}
}

// classify maps a transport error onto the sentinels above.
//
// The SDK collapses status codes into a generic error, so the status is
// recovered from the message. That is fragile in principle; in practice the
// distinction it buys, credential versus model, is the one worth having, and
// an unmatched error falls back to ErrUnreachable rather than guessing.
func classify(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrUnreachable
	}
	message := strings.ToLower(err.Error())
	switch {
	case contains(message, "unauthorized"), contains(message, "401"),
		contains(message, "invalid_api_key"), contains(message, "no auth credentials"),
		contains(message, "user not found"):
		return ErrBadCredential
	case contains(message, "404"), contains(message, "not found"),
		contains(message, "no model"), contains(message, "not available"):
		return ErrUnknownModel
	default:
		return ErrUnreachable
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// Summarize turns a probe into the single line the interface displays.
func Summarize(results []ProbeResult) (string, bool) {
	if len(results) == 0 {
		return "no model configured", false
	}
	var bad []string
	for _, r := range results {
		if r.Err == nil {
			return fmt.Sprintf("%s is reachable", r.Model), true
		}
		bad = append(bad, r.Err.Error())
	}
	return bad[0], false
}
