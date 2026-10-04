// Package store manages SQLite database connections, WAL pragmas, and schema migrations.
//
// ==============================================================================
// ARCHITECTURAL ROLE & SQLITE CONCURRENCY DESIGN:
// High-Concurrency SQLite Architecture (NFR-7, CLI-6, CLI-7).
//
//  1. Pure Go SQLite Driver (No CGO!):
//     `_ "modernc.org/sqlite"` is a pure Go transpilation of the C SQLite engine.
//     This allows Garagefab to compile with `CGO_ENABLED=0`, producing a static binary
//     that runs on any Linux/macOS machine without needing C libraries or GCC.
//
//  2. Dual Connection Pool (Single Writer + Multi Reader):
//     SQLite is fundamentally single-writer. In standard default mode, writes lock readers.
//     We solve this with a dual-pool architecture:
//     - `writeDB.SetMaxOpenConns(1)`: A dedicated, serialized write connection.
//     - `readDB.SetMaxOpenConns(10)`: A pool of up to 10 concurrent read connections.
//
// 3. WAL Mode Pragmas (Write-Ahead Logging):
//
//   - `PRAGMA journal_mode=WAL;`: Allows readers to read while writers write!
//
//   - `PRAGMA busy_timeout=5000;`: Retries for up to 5 seconds if a lock is contested.
//
//   - `PRAGMA synchronous=NORMAL;`: Optimized durability without syncing on every commit.
//
//   - `PRAGMA foreign_keys=ON;`: Enables cascading deletes and foreign key constraints.
//
//     4. Embedded Migrations (Goose vs Java Flyway/Liquibase):
//     In Java/Spring: Flyway reads migrations from `src/main/resources/db/migration/`.
//     In Go: `//go:embed migrations/*.sql` embeds SQL files into the binary, and `goose.Up()`
//     applies migrations automatically on startup.
//
// ==============================================================================
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
	_ "modernc.org/sqlite" // Pure Go SQLite driver (CGO_ENABLED=0 compliant)
)

// embedMigrations embeds all SQL migration files in the migrations directory into the binary.
//
//go:embed migrations/*.sql
var embedMigrations embed.FS

// ExpectedSchemaVersion is the maximum schema version this binary knows how to handle.
const ExpectedSchemaVersion = 3

// DB wraps separate write and read connection pools for SQLite.
type DB struct {
	writeDB *sql.DB // Single serialized connection for all write/mutation queries
	readDB  *sql.DB // Multi-connection pool for concurrent read-only queries
}

// Open initializes SQLite databases with PRAGMA settings, applies goose migrations,
// and validates the schema version against the binary.
func Open(dbPath string) (*DB, error) {
	// Ensure database directory exists with owner-only permissions (0700)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, fmt.Errorf("store: create db directory: %w", err)
	}

	// Data Source Name (DSN) configuring SQLite PRAGMAs
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", dbPath)

	// 1. Open write connection (serialized single connection to prevent SQLite locking collisions)
	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open write db: %w", err)
	}
	writeDB.SetMaxOpenConns(1)

	// 2. Open read pool (up to 10 concurrent reader connections)
	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("store: open read db: %w", err)
	}
	readDB.SetMaxOpenConns(10)

	// 3. Apply PRAGMAs explicitly on both connection pools
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

	// 4. Run embedded goose migrations on write connection (like Flyway in Java)
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

	// 5. Schema version check (CLI-7): Refuse startup if DB was migrated by a newer binary
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
