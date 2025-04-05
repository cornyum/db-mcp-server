package repository

import (
	"context"
	"database/sql"
	"strings"

	"github.com/cornyum/db-mcp-server/internal/domain"
	"github.com/cornyum/db-mcp-server/pkg/dbtools"
)

// TODO: Implement caching layer for database metadata to improve performance
// TODO: Add observability with tracing and detailed metrics
// TODO: Improve concurrency handling with proper locking or atomic operations
// TODO: Consider using an interface-based approach for better testability
// TODO: Add comprehensive integration tests for different database types

// DatabaseRepository implements domain.DatabaseRepository
type DatabaseRepository struct{}

// NewDatabaseRepository creates a new database repository
func NewDatabaseRepository() *DatabaseRepository {
	return &DatabaseRepository{}
}

// GetDatabase retrieves a database by ID
func (r *DatabaseRepository) GetDatabase(id string) (domain.Database, error) {
	db, err := dbtools.GetDatabase(id)
	if err != nil {
		return nil, err
	}
	return &DatabaseAdapter{db: db}, nil
}

// ListDatabases returns a list of available database IDs
func (r *DatabaseRepository) ListDatabases() []string {
	return dbtools.ListDatabases()
}

// GetDatabaseType returns the type of a database by ID
func (r *DatabaseRepository) GetDatabaseType(id string) (string, error) {
	// Simple approach - infer type from database ID
	// This is a temporary solution until we have a proper way to get the connection info
	switch {
	case strings.HasPrefix(id, "postgres"):
		return "postgres", nil
	case strings.HasPrefix(id, "mysql"):
		return "mysql", nil
	default:
		// For unknown types, we can try to execute a database-specific query
		// to identify the type, but for now we'll default to mysql
		return "mysql", nil
	}
}

// DatabaseAdapter adapts the db.Database to the domain.Database interface
type DatabaseAdapter struct {
	db interface {
		Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
		Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
		BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	}
}

// Query executes a query on the database
func (a *DatabaseAdapter) Query(ctx context.Context, query string, args ...interface{}) (domain.Rows, error) {
	rows, err := a.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &RowsAdapter{rows: rows}, nil
}

// Exec executes a statement on the database
func (a *DatabaseAdapter) Exec(ctx context.Context, statement string, args ...interface{}) (domain.Result, error) {
	result, err := a.db.Exec(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	return &ResultAdapter{result: result}, nil
}

// Begin starts a new transaction
func (a *DatabaseAdapter) Begin(ctx context.Context, opts *domain.TxOptions) (domain.Tx, error) {
	txOpts := &sql.TxOptions{}
	if opts != nil {
		txOpts.ReadOnly = opts.ReadOnly
	}

	tx, err := a.db.BeginTx(ctx, txOpts)
	if err != nil {
		return nil, err
	}
	return &TxAdapter{tx: tx}, nil
}

// RowsAdapter adapts sql.Rows to domain.Rows
type RowsAdapter struct {
	rows *sql.Rows
}

// Close closes the rows
func (a *RowsAdapter) Close() error {
	return a.rows.Close()
}

// Columns returns the column names
func (a *RowsAdapter) Columns() ([]string, error) {
	return a.rows.Columns()
}

// Next advances to the next row
func (a *RowsAdapter) Next() bool {
	return a.rows.Next()
}

// Scan scans the current row
func (a *RowsAdapter) Scan(dest ...interface{}) error {
	return a.rows.Scan(dest...)
}

// Err returns any error that occurred during iteration
func (a *RowsAdapter) Err() error {
	return a.rows.Err()
}

// ResultAdapter adapts sql.Result to domain.Result
type ResultAdapter struct {
	result sql.Result
}

// RowsAffected returns the number of rows affected
func (a *ResultAdapter) RowsAffected() (int64, error) {
	return a.result.RowsAffected()
}

// LastInsertId returns the last insert ID
func (a *ResultAdapter) LastInsertId() (int64, error) {
	return a.result.LastInsertId()
}

// TxAdapter adapts sql.Tx to domain.Tx
type TxAdapter struct {
	tx *sql.Tx
}

// Commit commits the transaction
func (a *TxAdapter) Commit() error {
	return a.tx.Commit()
}

// Rollback rolls back the transaction
func (a *TxAdapter) Rollback() error {
	return a.tx.Rollback()
}

// Query executes a query within the transaction
func (a *TxAdapter) Query(ctx context.Context, query string, args ...interface{}) (domain.Rows, error) {
	rows, err := a.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &RowsAdapter{rows: rows}, nil
}

// Exec executes a statement within the transaction
func (a *TxAdapter) Exec(ctx context.Context, statement string, args ...interface{}) (domain.Result, error) {
	result, err := a.tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	return &ResultAdapter{result: result}, nil
}
