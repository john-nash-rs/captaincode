package storage

import (
	"context"
	"time"
)

// Turn is one exchange in a conversation: the prompt and the answer it drew.
//
// It is stored separately from the run snapshot because the snapshot is
// overwritten every time the conversation grows, so it can only ever describe
// the newest state. The interface pages back through what actually happened,
// which needs the turns kept rather than replaced.
type Turn struct {
	Question string
	Answer   string
}

// AppendTurn records one exchange against its conversation.
//
// ordinal is the position of the prompt in the conversation, counted from one,
// and it is the primary key alongside the run id. Writing the same ordinal
// twice replaces it rather than failing, so a retry after a crash cannot leave
// two copies of one exchange.
func (d *DB) AppendTurn(ctx context.Context, runID string, ordinal int, question, answer string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := d.sql.ExecContext(ctx, `INSERT INTO turns(run_id,ordinal,question,answer,recorded) VALUES(?,?,?,?,?)
ON CONFLICT(run_id,ordinal) DO UPDATE SET question=excluded.question,answer=excluded.answer,recorded=excluded.recorded`,
		runID, ordinal, question, answer, now)
	return err
}

// Turns returns a page of a conversation's turns, oldest first.
//
// skip counts back from the newest turn, which is what the interface has
// already loaded, so a reader asks for what comes before what it holds without
// having to track absolute positions of its own. more reports whether a further
// page exists, so paging can stop at the beginning rather than querying until
// an empty page comes back.
func (d *DB) Turns(ctx context.Context, runID string, skip, limit int) ([]Turn, bool, error) {
	if runID == "" || limit <= 0 {
		return nil, false, nil
	}
	rows, err := d.sql.QueryContext(ctx, `SELECT question, answer FROM turns WHERE run_id=? ORDER BY ordinal DESC LIMIT ? OFFSET ?`,
		runID, limit+1, skip)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var newestFirst []Turn
	for rows.Next() {
		var turn Turn
		if err := rows.Scan(&turn.Question, &turn.Answer); err != nil {
			return nil, false, err
		}
		newestFirst = append(newestFirst, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(newestFirst) > limit
	if more {
		newestFirst = newestFirst[:limit]
	}
	// The query walks backwards so the limit applies to the newest turns; the
	// page is reversed so the caller can prepend it above what it holds.
	page := make([]Turn, len(newestFirst))
	for i, turn := range newestFirst {
		page[len(page)-1-i] = turn
	}
	return page, more, nil
}
