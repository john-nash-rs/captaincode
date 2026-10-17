package main

import (
	"context"
	loop "example.com/agentbook/checkpoints/02-loop"
	"fmt"
	"log"
)

type fixture struct{}

func (fixture) Complete(_ context.Context, history []loop.Message) (loop.Message, error) {
	if len(history) == 1 {
		return loop.Message{Role: "assistant", Calls: []loop.Call{{ID: "read-1", Name: "read_file", Arguments: `{"path":"package.json"}`}}}, nil
	}
	return loop.Message{Role: "assistant", Content: "The version is 2.5.0."}, nil
}

func main() {
	history, err := loop.Run(context.Background(), fixture{}, func(_ context.Context, call loop.Call) (string, error) {
		if call.Name != "read_file" || call.Arguments != `{"path":"package.json"}` {
			return "", fmt.Errorf("unsupported fixture request")
		}
		return `{"name":"workshop","version":"2.5.0"}`, nil
	}, "What is the project version?", 3)
	if err != nil {
		log.Fatal(err)
	}
	for _, message := range history {
		fmt.Printf("%s call=%s calls=%v content=%s\n", message.Role, message.CallID, message.Calls, message.Content)
	}
}
