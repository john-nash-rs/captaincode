package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"example.com/agentbook/core"
	openrouter "github.com/OpenRouterTeam/go-sdk"
	"github.com/OpenRouterTeam/go-sdk/models/components"
	"github.com/OpenRouterTeam/go-sdk/retry"
)

type OpenRouter struct {
	Client *openrouter.OpenRouter
	Model  string
	Stream bool
	OnText func(string)
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

func New(key, model, server string) *OpenRouter {
	options := []openrouter.SDKOption{openrouter.WithSecurity(key), openrouter.WithRetryConfig(retry.Config{Strategy: "none"}), openrouter.WithClient(&http.Client{Timeout: 60 * time.Second, Transport: cappedTransport{base: http.DefaultTransport}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }})}
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
	if p.Model == "" {
		return core.Message{}, errors.New("configure an explicit OpenRouter model ID")
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
	data, err := json.Marshal(map[string]any{"model": p.Model, "messages": messages, "tools": tools, "stream": p.Stream, "max_tokens": 2048})
	if err != nil {
		return core.Message{}, err
	}
	var request components.ChatRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return core.Message{}, errors.New("provider request conversion failed")
	}
	response, err := p.Client.Chat.Send(ctx, request, nil)
	if err != nil {
		return core.Message{}, errors.New("OpenRouter request failed; inspect redacted transport diagnostics")
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
	message := core.Message{Role: "assistant", Content: wire.Content, ProviderData: wire.ReasoningDetails}
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
