// Package orders is a single-tenant, local teaching backend, not a payment API.
package orders

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	_ "modernc.org/sqlite"
)

var ErrConflict = errors.New("idempotency key used with different request")

type Backend struct {
	DB            *sql.DB
	ResponseDelay time.Duration
}
type Order struct {
	ID     int64  `json:"id"`
	Item   string `json:"item"`
	Status string `json:"status"`
}

func Open(path string) (*Backend, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	_, err = database.Exec(`CREATE TABLE IF NOT EXISTS orders(id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,request_hash TEXT NOT NULL,item TEXT NOT NULL,status TEXT NOT NULL)`)
	if err != nil {
		database.Close()
		return nil, err
	}
	return &Backend{DB: database}, nil
}

func (b *Backend) Create(ctx context.Context, key, item string) (Order, error) {
	var order Order
	if key == "" || len(key) > 128 || item == "" || len(item) > 200 {
		return order, errors.New("invalid order")
	}
	hash := sha256.Sum256([]byte(item))
	fingerprint := hex.EncodeToString(hash[:])
	transaction, err := b.DB.BeginTx(ctx, nil)
	if err != nil {
		return order, err
	}
	defer transaction.Rollback()
	_, err = transaction.ExecContext(ctx, `INSERT INTO orders(idempotency_key,request_hash,item,status) VALUES(?,?,?,'completed') ON CONFLICT(idempotency_key) DO NOTHING`, key, fingerprint, item)
	if err != nil {
		return order, err
	}
	var storedHash string
	err = transaction.QueryRowContext(ctx, `SELECT id,request_hash,item,status FROM orders WHERE idempotency_key=?`, key).Scan(&order.ID, &storedHash, &order.Item, &order.Status)
	if err != nil {
		return order, err
	}
	if storedHash != fingerprint {
		return order, ErrConflict
	}
	return order, transaction.Commit()
}

func (b *Backend) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != "POST" || request.URL.Path != "/orders" {
		http.NotFound(writer, request)
		return
	}
	var input struct {
		Item string `json:"item"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		http.Error(writer, "invalid request", 400)
		return
	}
	order, err := b.Create(request.Context(), request.Header.Get("Idempotency-Key"), input.Item)
	if errors.Is(err, ErrConflict) {
		http.Error(writer, "key conflict", 409)
		return
	}
	if err != nil {
		http.Error(writer, "order failed", 400)
		return
	}
	// Commit has already succeeded. Losing the reply cannot undo that commit.
	if b.ResponseDelay > 0 {
		time.Sleep(b.ResponseDelay)
	}
	writer.Header().Set("Content-Type", "application/json")
	fmt.Fprintln(writer, mustJSON(order))
}
func mustJSON(value any) string { data, _ := json.Marshal(value); return string(data) }
