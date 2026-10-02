// Package store provides transaction management and unified query execution interfaces.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Unit of Work Pattern & Explicit Transaction Closures (PIP-2).
//
// GO CONCEPTS & JAVA / SPRING @TRANSACTIONAL COMPARISONS:
//
//  1. Unified Executor Interface (`dbtx`):
//     `*sql.DB` and `*sql.Tx` have identical query method signatures (`ExecContext`,
//     `QueryContext`, `QueryRowContext`), but they do not share a common interface in Go's standard library.
//     We define `dbtx` so our repository structs (`ProjectRepo`, `JobRepo`, etc.) can
//     execute queries against EITHER a standalone database pool OR an active transaction
//     without duplicating code!
//
// 2. Transaction Closures (`WithTx` vs Spring `@Transactional`):
//   - In Spring Boot: Transactions use AOP proxies (`@Transactional`) and thread-local
//     state (`ThreadLocal<TransactionStatus>`). If an exception is thrown, Spring rolls back.
//   - In Go: We avoid thread-locals because goroutines are not threads.
//     Instead, we use a functional transaction closure:
//     err := db.WithTx(ctx, func(tx *store.Tx) error { ... })
//     If the closure returns a non-nil error, `WithTx` calls `sqlTx.Rollback()`;
//     if nil, it calls `sqlTx.Commit()`.
//
// ==============================================================================
package store

import (
	"context"
	"database/sql"
	"fmt"
)

// dbtx is the common interface implemented by dbExecutor and txExecutor.
// It allows repository queries to execute transparently on a DB connection or within a Tx.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// dbExecutor routes writes to writeDB and reads to readDB.
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

// txExecutor routes all queries to the active *sql.Tx transaction.
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

// WithTx executes the given function in an atomic database transaction (PIP-2).
// If fn returns an error, the transaction is rolled back; otherwise committed.
func (db *DB) WithTx(ctx context.Context, fn func(tx *Tx) error) error {
	// Begin transaction on the serialized write connection
	sqlTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}

	tx := &Tx{
		tx:   sqlTx,
		exec: &txExecutor{tx: sqlTx},
	}

	// Execute user closure
	if err := fn(tx); err != nil {
		// Rollback on any returned error
		if rbErr := sqlTx.Rollback(); rbErr != nil {
			return fmt.Errorf("store: rollback failed (%v) after error: %w", rbErr, err)
		}
		return err
	}

	// Commit on success
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("store: commit tx: %w", err)
	}

	return nil
}

// ------------------------------------------------------------------------------
// Repository accessors on DB (non-transactional / auto-commit)
// ------------------------------------------------------------------------------

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

// ------------------------------------------------------------------------------
// Repository accessors on Tx (transactional)
// ------------------------------------------------------------------------------

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
