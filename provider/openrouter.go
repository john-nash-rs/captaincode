package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	openrouter "github.com/OpenRouterTeam/go-sdk"
	"github.com/OpenRouterTeam/go-sdk/models/components"
	"github.com/OpenRouterTeam/go-sdk/retry"
	"github.com/john-nash-rs/captaincode/core"
)

type OpenRouter struct {
	Client *openrouter.OpenRouter
	Model  string
	Stream bool
	OnText func(string)
	// ModelFunc, when set, supplies the model identifier per request so that
	// changing it from the terminal interface takes effect immediately. The
	// stored Model is used when it is nil.
	ModelFunc func() string
}

// model returns the identifier to use for one request.
func (p *OpenRouter) model() string {
	if p.ModelFunc != nil {
		return p.ModelFunc()
	}
	return p.Model
}

type cappedTransport struct{ base http.RoundTripper }
type cappedBody struct {
	io.Reader
	io.Closer
}

func (t cappedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err == nil {
		response.Body = cappedBody{Reader: io.LimitReader(response.Body, 2<<20), Closer: response.Body}
	}
	return response, err
}

// KeySource returns the credential for one request. It is consulted per call
// rather than captured at construction so a credential stored from the
// terminal interface takes effect on the next request without a restart.
type KeySource func(context.Context) (string, error)

func New(key, model, server string) *OpenRouter {
	return NewWithKey(func(context.Context) (string, error) { return key, nil }, model, server)
}

// NewWithKey builds an adapter that resolves its credential on every request.
func NewWithKey(source KeySource, model, server string) *OpenRouter {
	security := func(ctx context.Context) (components.Security, error) {
		key, err := source(ctx)
		if err != nil {
			return components.Security{}, err
		}
		if key == "" {
			return components.Security{}, errors.New("no provider credential is configured")
		}
		return components.Security{APIKey: &key}, nil
	}
	options := []openrouter.SDKOption{openrouter.WithSecuritySource(security), openrouter.WithRetryConfig(retry.Config{Strategy: "none"}), openrouter.WithClient(&http.Client{Timeout: 60 * time.Second, Transport: cappedTransport{base: http.DefaultTransport}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }})}
	if server != "" {
		options = append(options, openrouter.WithServerURL(server))
	}
	return &OpenRouter{Client: openrouter.New(options...), Model: model}
}

type wireCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}
type wireMessage struct {
	Role             string          `json:"role"`
	Content          string          `json:"content,omitempty"`
	ToolCalls        []wireCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
}

func (p *OpenRouter) Complete(ctx context.Context, history []core.Message, definitions []core.Definition) (core.Message, error) {
	if p.model() == "" {
		// The bare version of this message used to be the whole of the
		// help offered, which named the problem but none of the ways to
		// fix it. Every path below is a complete remedy on its own.
		return core.Message{}, errors.New("no OpenRouter model is configured; set one with:\n" +
			"  --model openrouter/free          for this run\n" +
			"  \"model\" in .captaincode.json     to persist it\n" +
			"  /openrouter model <id>           inside the interface\n" +
			"  captaincode doctor --provider openrouter   to see which file is in force")
	}
	messages := make([]wireMessage, 0, len(history))
	for _, message := range history {
		wire := wireMessage{Role: message.Role, Content: message.Content, ToolCallID: message.CallID, ReasoningDetails: message.ProviderData}
		for _, call := range message.Calls {
			item := wireCall{ID: call.ID, Type: "function"}
			item.Function.Name = call.Name
			item.Function.Arguments = string(call.Arguments)
			wire.ToolCalls = append(wire.ToolCalls, item)
		}
		messages = append(messages, wire)
	}
	tools := make([]any, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, map[string]any{"type": "function", "function": definition})
	}
	data, err := json.Marshal(map[string]any{"model": p.model(), "messages": messages, "tools": tools, "stream": p.Stream, "max_tokens": 2048})
	if err != nil {
		return core.Message{}, err
	}
	var request components.ChatRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return core.Message{}, errors.New("provider request conversion failed")
	}
	response, err := p.Client.Chat.Send(ctx, request, nil)
	if err != nil {
		// A described failure, carrying the provider's own wording and a
		// remedy. The old text said only "inspect redacted transport
		// diagnostics", which named neither the cause nor the way out, and was
		// identical for a missing model, a refused key and a rate limit.
		return core.Message{}, parseAPIError(p.model(), err)
	}
	if response == nil {
		return core.Message{}, errors.New("empty provider response")
	}
	if response.EventStream != nil {
		defer response.EventStream.Close()
		assembler := Assembler{}
		for response.EventStream.Next() {
			value := response.EventStream.Value()
			if value == nil {
				return core.Message{}, errors.New("empty stream event")
			}
			if value.Data.Error != nil {
				return core.Message{}, errors.New("provider reported a streaming error")
			}
			data, err := json.Marshal(value.Data)
			if err != nil {
				return core.Message{}, err
			}
			var chunk Chunk
			if err := json.Unmarshal(data, &chunk); err != nil {
				return core.Message{}, err
			}
			if err := assembler.Add(chunk, p.OnText); err != nil {
				return core.Message{}, err
			}
		}
		if err := response.EventStream.Err(); err != nil {
			return core.Message{}, errors.New("incomplete provider stream")
		}
		return assembler.Finish()
	}
	if response.ChatResult == nil {
		return core.Message{}, errors.New("missing completion")
	}
	data, err = json.Marshal(response.ChatResult)
	if err != nil {
		return core.Message{}, err
	}
	var result struct {
		Choices []struct {
			Message      wireMessage `json:"message"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return core.Message{}, err
	}
	if len(result.Choices) != 1 {
		return core.Message{}, errors.New("expected exactly one completion choice")
	}
	choice := result.Choices[0]
	return completeMessage(choice.Message, choice.FinishReason)
}

func completeMessage(wire wireMessage, reason string) (core.Message, error) {
	if reason != "stop" && reason != "tool_calls" {
		return core.Message{}, errors.New("completion did not finish normally")
	}
	// Providers emit leading and trailing whitespace inconsistently, and some
	// prefix replies with blank lines. Left in place it shows up as empty rows
	// above every answer in the interface and as stray newlines in one-shot
	// output. Trimming here, where the text first enters, means nothing
	// downstream has to know which model happened to be chosen.
	message := core.Message{Role: "assistant", Content: strings.TrimSpace(wire.Content), ProviderData: wire.ReasoningDetails}
	for _, call := range wire.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			return core.Message{}, errors.New("unsupported tool type")
		}
		if !json.Valid([]byte(call.Function.Arguments)) {
			return core.Message{}, errors.New("incomplete tool arguments")
		}
		message.Calls = append(message.Calls, core.Call{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
	}
	if reason == "tool_calls" && len(message.Calls) == 0 {
		return core.Message{}, errors.New("tool completion without calls")
	}
	return message, nil
}
