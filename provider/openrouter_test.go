package provider

import (
	"context"
	"encoding/json"
	"example.com/agentbook/core"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSDKAdapterPreservesToolProtocol(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing authentication")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		messages := body["messages"].([]any)
		if messages[2].(map[string]any)["tool_call_id"] != "read-1" {
			t.Error("call ID lost")
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Write([]byte(`{"id":"fixture","object":"chat.completion","created":1,"model":"test-model","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"2.5.0"}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`))
	}))
	defer server.Close()
	client := New("test-key", "test-model", server.URL)
	reply, err := client.Complete(context.Background(), []core.Message{{Role: "user", Content: "version?"}, {Role: "assistant", Calls: []core.Call{{ID: "read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"package.json"}`)}}}, {Role: "tool", CallID: "read-1", Content: `{"version":"2.5.0"}`}}, nil)
	if err != nil || reply.Content != "2.5.0" || requests.Load() != 1 {
		t.Fatal(reply, err, requests.Load())
	}
}
func TestSDKDoesNotMultiplyRetries(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(503)
		writer.Write([]byte(`{"error":{"message":"temporarily unavailable","code":503}}`))
	}))
	defer server.Close()
	_, err := New("test-key", "test-model", server.URL).Complete(context.Background(), []core.Message{{Role: "user", Content: "test"}}, nil)
	if err == nil || requests.Load() != 1 {
		t.Fatal("SDK unexpectedly retried", requests.Load(), err)
	}
}
func TestStreamNeedsCompletionAndValidArguments(t *testing.T) {
	var assembler Assembler
	inputs := []string{`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"read-1","type":"function","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"package.json\"}"}}]},"finish_reason":"tool_calls"}]}`}
	var first Chunk
	json.Unmarshal([]byte(inputs[0]), &first)
	if err := assembler.Add(first, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := assembler.Finish(); err == nil {
		t.Fatal("partial stream became executable")
	}
	var last Chunk
	json.Unmarshal([]byte(inputs[1]), &last)
	if err := assembler.Add(last, nil); err != nil {
		t.Fatal(err)
	}
	message, err := assembler.Finish()
	if err != nil || len(message.Calls) != 1 || message.Calls[0].ID != "read-1" {
		t.Fatal(message, err)
	}
}
