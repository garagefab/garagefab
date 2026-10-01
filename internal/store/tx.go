package store

import (
	"context"
	"database/sql"
	"fmt"
)

// dbtx is the common interface implemented by dbExecutor and txExecutor.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type dbExecutor struct {
	write *sql.DB
	read  *sql.DB
}

func (e *dbExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return e.write.ExecContext(ctx, query, args...)
}

func (e *dbExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return e.read.QueryContext(ctx, query, args...)
}

func (e *dbExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return e.read.QueryRowContext(ctx, query, args...)
}

type txExecutor struct {
	tx *sql.Tx
}

func (e *txExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return e.tx.ExecContext(ctx, query, args...)
}

func (e *txExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return e.tx.QueryContext(ctx, query, args...)
}

func (e *txExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return e.tx.QueryRowContext(ctx, query, args...)
}

// Tx wraps an active transaction on the write connection.
type Tx struct {
	tx   *sql.Tx
	exec dbtx
}

// WithTx executes the given function in a database transaction (PIP-2).
// If fn returns an error, the transaction is rolled back; otherwise committed.
func (db *DB) WithTx(ctx context.Context, fn func(tx *Tx) error) error {
	sqlTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}

	tx := &Tx{
		tx:   sqlTx,
		exec: &txExecutor{tx: sqlTx},
	}

	if err := fn(tx); err != nil {
		if rbErr := sqlTx.Rollback(); rbErr != nil {
			return fmt.Errorf("store: rollback failed (%v) after error: %w", rbErr, err)
		}
		return err
	}

	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("store: commit tx: %w", err)
	}

	return nil
}

// Repositories on DB

func (db *DB) Projects() *ProjectRepo {
	return &ProjectRepo{q: &dbExecutor{write: db.writeDB, read: db.readDB}}
}

func (db *DB) Jobs() *JobRepo {
	return &JobRepo{q: &dbExecutor{write: db.writeDB, read: db.readDB}}
}

func (db *DB) StepRuns() *StepRunRepo {
	return &StepRunRepo{q: &dbExecutor{write: db.writeDB, read: db.readDB}}
}

func (db *DB) Events() *EventRepo {
	return &EventRepo{q: &dbExecutor{write: db.writeDB, read: db.readDB}}
}

func (db *DB) Approvals() *ApprovalRepo {
	return &ApprovalRepo{q: &dbExecutor{write: db.writeDB, read: db.readDB}}
}

func (db *DB) Sessions() *SessionRepo {
	return &SessionRepo{q: &dbExecutor{write: db.writeDB, read: db.readDB}}
}

func (db *DB) ProcessRecords() *ProcessRecordRepo {
	return &ProcessRecordRepo{q: &dbExecutor{write: db.writeDB, read: db.readDB}}
}

// Repositories on Tx

func (tx *Tx) Projects() *ProjectRepo {
	return &ProjectRepo{q: tx.exec}
}

func (tx *Tx) Jobs() *JobRepo {
	return &JobRepo{q: tx.exec}
}

func (tx *Tx) StepRuns() *StepRunRepo {
	return &StepRunRepo{q: tx.exec}
}

func (tx *Tx) Events() *EventRepo {
	return &EventRepo{q: tx.exec}
}

func (tx *Tx) Approvals() *ApprovalRepo {
	return &ApprovalRepo{q: tx.exec}
}

func (tx *Tx) Sessions() *SessionRepo {
	return &SessionRepo{q: tx.exec}
}

func (tx *Tx) ProcessRecords() *ProcessRecordRepo {
	return &ProcessRecordRepo{q: tx.exec}
}
