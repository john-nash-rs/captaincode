package orders

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLostHTTPReplyDoesNotUndoCommittedOrder(t *testing.T) {
	backend, err := Open(filepath.Join(t.TempDir(), "orders.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer backend.DB.Close()
	committed := make(chan Order, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order, err := backend.Create(r.Context(), r.Header.Get("Idempotency-Key"), "book")
		if err != nil {
			http.Error(w, "failed", 500)
			return
		}
		committed <- order
		<-release
		json.NewEncoder(w).Encode(order)
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(`{"item":"book"}`))
	request.Header.Set("Idempotency-Key", "checkout-lost-reply")
	finished := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if response != nil {
			response.Body.Close()
		}
		finished <- err
	}()
	var original Order
	select {
	case original = <-committed:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not commit")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("expected interrupted reply")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client ignored cancellation")
	}
	replay, err := backend.Create(context.Background(), "checkout-lost-reply", "book")
	if err != nil || replay.ID != original.ID {
		t.Fatal("same intent created different order", err)
	}
	var count int
	backend.DB.QueryRow("SELECT count(*) FROM orders").Scan(&count)
	if count != 1 {
		t.Fatal("duplicate order", count)
	}
}
