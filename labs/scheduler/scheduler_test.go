package scheduler

import (
	"context"
	"example.com/agentbook/core"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedConcurrencyAndCorrelation(t *testing.T) {
	var active, peak atomic.Int32
	var calls []core.Call
	for index := 0; index < 9; index++ {
		calls = append(calls, core.Call{ID: fmt.Sprint(index)})
	}
	results, err := Batch(context.Background(), calls, 3, func(_ context.Context, call core.Call) core.Result {
		value := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); value > old; old = peak.Load() {
			if peak.CompareAndSwap(old, value) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		return core.Result{OK: true, Text: call.ID}
	})
	if err != nil || len(results) != 9 || peak.Load() > 3 {
		t.Fatal("scheduler violated bound", peak.Load(), err)
	}
	for id, result := range results {
		if id != result.Text {
			t.Fatal("lost correlation")
		}
	}
}
