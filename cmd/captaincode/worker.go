package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/john-nash-rs/captaincode/sandbox"
)

// runWorker executes one tool call inside the sandbox and writes the result to
// stdout as JSON.
//
// This is the path the harness takes when a tool runs: the parent re-executes
// this binary as a sandboxed child with the argument "worker" and an encoded
// envelope on stdin. It is deliberately isolated in its own file so the
// re-execution contract is easy to review.
//
// The envelope is size-limited because it crosses a trust boundary: the caller
// is this same binary, but the arguments inside describe a model proposal.
func runWorker() error {
	var envelope sandbox.Envelope
	data, err := io.ReadAll(io.LimitReader(stdin(), 65537))
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return errors.New("worker request too large")
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	return json.NewEncoder(stdout()).Encode(envelope.Registry.Execute(context.Background(), envelope.Call))
}
