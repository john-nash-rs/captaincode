package orders

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentDuplicatesPersistOneOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orders.db")
	backend, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	ids := make(chan int64, 12)
	for index := 0; index < 12; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			order, err := backend.Create(context.Background(), "checkout-abc123", "book")
			if err != nil {
				t.Error(err)
				return
			}
			ids <- order.ID
		}()
	}
	wait.Wait()
	close(ids)
	var first int64
	for id := range ids {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatal("duplicate business operation")
		}
	}
	var count int
	if err := backend.DB.QueryRow(`SELECT count(*) FROM orders`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err := backend.Create(context.Background(), "checkout-abc123", "different book"); !errors.Is(err, ErrConflict) {
		t.Fatal("mismatched payload accepted")
	}
	backend.DB.Close()
	backend, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.DB.Close()
	order, err := backend.Create(context.Background(), "checkout-abc123", "book")
	if err != nil || order.ID != first {
		t.Fatal("retry after restart lost identity")
	}
}
