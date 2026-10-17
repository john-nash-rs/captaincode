package provider

import (
	"context"
	"fmt"
	"github.com/john-nash-rs/captaincode/core"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSDKStreamingTransport(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(writer, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
				if complete {
					fmt.Fprint(writer, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer server.Close()
			client := New("test-key", "test-model", server.URL)
			client.Stream = true
			message, err := client.Complete(context.Background(), []core.Message{{Role: "user", Content: "test"}}, nil)
			if complete && (err != nil || message.Content != "hello") {
				t.Fatal(message, err)
			}
			if !complete && err == nil {
				t.Fatal("truncated stream accepted")
			}
		})
	}
}
