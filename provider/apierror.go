package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// APIError is a failure the provider described, rather than a transport fault.
//
// The previous wording for every failure was "inspect redacted transport
// diagnostics", which named no cause and no remedy. It was shown for a model
// that does not exist, a key that was refused, and a rate limit alike, so the
// only way to find out what went wrong was to read the raw body out of a log.
// The provider returns that body on the error, so it is parsed here and turned
// into something actionable.
type APIError struct {
	Code int
	// Message is the provider's own wording, which is often specific enough to
	// act on and clearer than any paraphrase.
	Message string
	// Model is the identifier in flight, so the message can name it.
	Model string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("provider returned %d", e.Code)
	}
	return e.Message
}

// Advice is what to do next, or an empty string when there is nothing useful to
// add beyond the message itself.
//
// Every case that a user can actually fix gets a remedy. An unrecognised one
// says so honestly rather than inventing one.
func (e *APIError) Advice() string {
	message := strings.ToLower(e.Message)
	switch {
	case e.Code == 401:
		return "The API key was rejected. Set a new one with /openrouter api-key, then /connect."
	case e.Code == 402:
		return "Out of credit. Add credit at openrouter.ai/credits, then /status."
	case e.Code == 429:
		return "Rate limited. Wait a moment and try again."
	case strings.Contains(message, "not a valid model"), strings.Contains(message, "no endpoints"),
		strings.Contains(message, "model not found"):
		return "That model does not exist. Pick another with /models."
	case strings.Contains(message, "agentic harness"), strings.Contains(message, "only available on"):
		// The free tier is gated to registered harnesses, and CaptainCode is
		// not one. Saying so is the whole point: the operator otherwise assumes
		// their key or their account is at fault.
		return "This free model is restricted to approved coding apps. Pick a paid one with /models."
	case strings.Contains(message, "no available"):
		return "That model has no provider available for your account right now. Try another with /models."
	case strings.Contains(message, "context length"), strings.Contains(message, "too long"),
		strings.Contains(message, "maximum context"):
		return "The conversation is longer than this model can accept. Start a new run, or pick a model with a larger context."
	case strings.Contains(message, "insufficient"), strings.Contains(message, "quota"):
		return "Your account cannot use that model. Pick another with /models."
	}
	if e.Code >= 400 {
		return "Run /status to re-check the connection, or pick another model with /models."
	}
	return ""
}

// parseAPIError extracts the provider's description from a failed request.
//
// The SDK surfaces the response body as the error text, which is JSON in the
// shape {"error":{"code":400,"message":"…"}}. Returning nil when the body is
// not that shape matters: an unparseable failure is still a failure, and must
// not be reported as a well-understood one.
func parseAPIError(model string, err error) *APIError {
	if err == nil {
		return nil
	}
	api := &APIError{Model: model}

	text := err.Error()
	// The code is also worth recovering on its own, because some bodies carry
	// it without the nested message.
	var envelope struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(text), &envelope) == nil {
		api.Code = envelope.Error.Code
		if api.Code == 0 {
			api.Code = envelope.Code
		}
		api.Message = envelope.Error.Message
		if api.Message == "" {
			api.Message = envelope.Message
		}
	}
	if api.Message == "" {
		// Not the expected shape. Fall back to classify, which recognises a
		// few status codes from their text.
		api.Code = statusFromText(text)
		api.Message = strings.TrimSpace(firstJSONLine(text))
	}
	if api.Message == "" {
		api.Message = "the request failed for an unrecognised reason"
	}
	return api
}

// statusFromText recovers an HTTP status from an error message that did not
// parse as the provider's envelope.
func statusFromText(text string) int {
	for _, code := range []int{400, 401, 402, 403, 404, 408, 429, 500, 502, 503} {
		if containsToken(text, code) {
			return code
		}
	}
	return 0
}

// containsToken reports whether the text mentions the number as a standalone
// token, so "400" does not match inside "1400".
func containsToken(text string, code int) bool {
	needle := fmt.Sprintf("%d", code)
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return r != '-' && (r < '0' || r > '9')
	}) {
		if field == needle {
			return true
		}
	}
	return false
}

func firstJSONLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	if len(text) > 200 {
		return text[:200] + "…"
	}
	return text
}

// adviceFor returns the remedy for any error, or an empty string.
func adviceFor(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Advice()
	}
	return ""
}
