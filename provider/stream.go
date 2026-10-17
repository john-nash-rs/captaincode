package provider

import (
	"encoding/json"
	"errors"
	"example.com/agentbook/core"
)

type Chunk struct {
	Choices []struct {
		Index        int         `json:"index"`
		Delta        wireMessage `json:"delta"`
		FinishReason *string     `json:"finish_reason"`
	} `json:"choices"`
}
type Assembler struct {
	message wireMessage
	calls   map[int]wireCall
	reason  string
	size    int
}

func (a *Assembler) Add(chunk Chunk, onText func(string)) error {
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			return errors.New("multiple streaming choices unsupported")
		}
		if a.reason != "" {
			return errors.New("data received after finish reason")
		}
		if len(choice.Delta.ReasoningDetails) > 0 && string(choice.Delta.ReasoningDetails) != "null" {
			return errors.New("streamed reasoning metadata unsupported; use non-streaming mode")
		}
		a.size += len(choice.Delta.Content)
		a.message.Content += choice.Delta.Content
		if onText != nil && choice.Delta.Content != "" {
			onText(choice.Delta.Content)
		}
		if a.calls == nil {
			a.calls = map[int]wireCall{}
		}
		for _, fragment := range choice.Delta.ToolCalls {
			if fragment.Type != "" && fragment.Type != "function" {
				return errors.New("unsupported streamed tool type")
			}
			if fragment.Index < 0 || fragment.Index >= 32 {
				return errors.New("invalid tool index")
			}
			call := a.calls[fragment.Index]
			if fragment.ID != "" {
				if call.ID != "" && call.ID != fragment.ID {
					return errors.New("tool ID changed midstream")
				}
				call.ID = fragment.ID
			}
			call.Type = fragment.Type
			call.Function.Name += fragment.Function.Name
			call.Function.Arguments += fragment.Function.Arguments
			a.size += len(fragment.Function.Name) + len(fragment.Function.Arguments) + len(fragment.ID)
			a.calls[fragment.Index] = call
		}
		if a.size > 131072 {
			return errors.New("stream exceeds assembly limit")
		}
		if choice.FinishReason != nil {
			a.reason = *choice.FinishReason
		}
	}
	return nil
}

func (a *Assembler) Finish() (core.Message, error) {
	if a.reason == "" {
		return core.Message{}, errors.New("stream ended before completion")
	}
	for index := 0; index < len(a.calls); index++ {
		call, exists := a.calls[index]
		if !exists || call.ID == "" || call.Function.Name == "" || !json.Valid([]byte(call.Function.Arguments)) {
			return core.Message{}, errors.New("incomplete streamed call")
		}
		a.message.ToolCalls = append(a.message.ToolCalls, call)
	}
	return completeMessage(a.message, a.reason)
}
