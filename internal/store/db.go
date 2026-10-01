package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

// ExpectedSchemaVersion is the maximum schema version this binary knows how to handle.
const ExpectedSchemaVersion = 2

// DB wraps separate write and read connection pools for SQLite.
type DB struct {
	writeDB *sql.DB
	readDB  *sql.DB
}

// Open initializes SQLite databases with PRAGMA settings, applies goose migrations,
// and validates the schema version against the binary.
func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, fmt.Errorf("store: create db directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", dbPath)

	// Open write connection (serialized single connection)
	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open write db: %w", err)
	}
	writeDB.SetMaxOpenConns(1)

	// Open read pool
	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("store: open read db: %w", err)
	}
	readDB.SetMaxOpenConns(10)

	// Apply PRAGMAs explicitly
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA foreign_keys=ON;",
	}
	for _, pragma := range pragmas {
		if _, err := writeDB.Exec(pragma); err != nil {
			writeDB.Close()
			readDB.Close()
			return nil, fmt.Errorf("store: apply pragma to write db %q: %w", pragma, err)
		}
		if _, err := readDB.Exec(pragma); err != nil {
			writeDB.Close()
			readDB.Close()
			return nil, fmt.Errorf("store: apply pragma to read db %q: %w", pragma, err)
		}
	}

	// Run embedded goose migrations on write connection
	goose.SetBaseFS(embedMigrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		writeDB.Close()
		readDB.Close()
		return nil, fmt.Errorf("store: goose set dialect: %w", err)
	}

	if err := goose.Up(writeDB, "migrations"); err != nil {
		writeDB.Close()
		readDB.Close()
		return nil, fmt.Errorf("store: apply migrations: %w", err)
	}

	// Schema version check (CLI-7)
	var versionStr string
	err = readDB.QueryRow("SELECT value FROM _meta WHERE key = 'schema_version'").Scan(&versionStr)
	if err != nil {
		writeDB.Close()
		readDB.Close()
		return nil, fmt.Errorf("store: read schema_version from _meta: %w", err)
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		writeDB.Close()
		readDB.Close()
		return nil, fmt.Errorf("store: parse schema_version %q: %w", versionStr, err)
	}

	if version > ExpectedSchemaVersion {
		writeDB.Close()
		readDB.Close()
		return nil, fmt.Errorf("store: schema version %d is newer than binary expects (max %d): update garagefab", version, ExpectedSchemaVersion)
	}

	return &DB{
		writeDB: writeDB,
		readDB:  readDB,
	}, nil
}

// Exec executes a query that modifies the database via the write connection.
func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.writeDB.Exec(query, args...)
}

// ExecContext executes a query that modifies the database with context via the write connection.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.writeDB.ExecContext(ctx, query, args...)
}

// Query executes a query that returns rows using the read pool.
func (db *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return db.readDB.Query(query, args...)
}

// QueryContext executes a query that returns rows with context using the read pool.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.readDB.QueryContext(ctx, query, args...)
}

// QueryRow executes a query expected to return at most one row using the read pool.
func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.readDB.QueryRow(query, args...)
}

// QueryRowContext executes a query expected to return at most one row with context using the read pool.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.readDB.QueryRowContext(ctx, query, args...)
}

// BeginTx starts a transaction on the write connection.
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return db.writeDB.BeginTx(ctx, opts)
}

// Close closes both write and read connection pools cleanly.
func (db *DB) Close() error {
	var errWrite, errRead error
	if db.writeDB != nil {
		errWrite = db.writeDB.Close()
	}
	if db.readDB != nil {
		errRead = db.readDB.Close()
	}
	if errWrite != nil {
		return errWrite
	}
	return errRead
}
