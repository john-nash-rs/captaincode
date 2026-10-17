package retry

import (
	"context"
	"errors"
	"time"
)

// Do is for operations whose repetition has been judged safe by the caller.
// attempts includes the first call. classify must reject permanent failures.
func Do(ctx context.Context, attempts int, delay time.Duration, classify func(error) bool, operation func(context.Context) error) error {
	if attempts < 1 || attempts > 10 || delay < 0 || delay > time.Minute {
		return errors.New("invalid retry policy")
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation(ctx)
		if err == nil || !classify(err) || attempt == attempts-1 {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
