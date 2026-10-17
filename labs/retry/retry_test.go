package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryExhaustionAndPermanentFailure(t *testing.T) {
	transient := errors.New("temporary")
	count := 0
	err := Do(context.Background(), 3, 0, func(err error) bool { return errors.Is(err, transient) }, func(context.Context) error { count++; return transient })
	if !errors.Is(err, transient) || count != 3 {
		t.Fatal(count, err)
	}
	count = 0
	_ = Do(context.Background(), 3, 0, func(error) bool { return false }, func(context.Context) error { count++; return errors.New("denied") })
	if count != 1 {
		t.Fatal("retried a permanent error")
	}
}
func TestCancelBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	count := 0
	err := Do(ctx, 3, time.Minute, func(error) bool { return true }, func(context.Context) error { count++; cancel(); return errors.New("retry") })
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatal(count, err)
	}
}
