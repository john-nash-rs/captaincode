package scheduler

import (
	"context"
	"errors"
	"sync"

	"example.com/agentbook/core"
)

// Batch accepts only independent operations already admitted by the caller.
// Each result retains its call ID; completion order does not define identity.
func Batch(ctx context.Context, calls []core.Call, limit int, execute func(context.Context, core.Call) core.Result) (map[string]core.Result, error) {
	if limit < 1 || limit > 32 {
		return nil, errors.New("invalid concurrency limit")
	}
	seen := map[string]bool{}
	for _, call := range calls {
		if call.ID == "" || seen[call.ID] {
			return nil, errors.New("duplicate or missing call ID")
		}
		seen[call.ID] = true
	}
	results := map[string]core.Result{}
	var mutex sync.Mutex
	jobs := make(chan core.Call)
	var workers sync.WaitGroup
	for index := 0; index < limit; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for call := range jobs {
				if ctx.Err() != nil {
					continue
				}
				result := execute(ctx, call)
				mutex.Lock()
				results[call.ID] = result
				mutex.Unlock()
			}
		}()
	}
dispatch:
	for _, call := range calls {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- call:
		}
	}
	close(jobs)
	workers.Wait()
	return results, ctx.Err()
}
