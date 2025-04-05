package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cornyum/db-mcp-server/pkg/logger"
	// Import database drivers
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

// Common database errors
var (
	ErrNotFound       = errors.New("record not found")
	ErrAlreadyExists  = errors.New("record already exists")
	ErrInvalidInput   = errors.New("invalid input")
	ErrNotImplemented = errors.New("not implemented")
	ErrNoDatabase     = errors.New("no database connection")
)

// Config represents database connection configuration
type Config struct {
	Type     string
	Host     string
	Port     int
	User     string
	Password string
	Name     string
	// Connection pool settings
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// SetDefaults sets default values for the configuration if they are not set
func (c *Config) SetDefaults() {
	if c.MaxOpenConns == 0 {
		c.MaxOpenConns = 25
	}
	if c.MaxIdleConns == 0 {
		c.MaxIdleConns = 5
	}
	if c.ConnMaxLifetime == 0 {
		c.ConnMaxLifetime = 5 * time.Minute
	}
	if c.ConnMaxIdleTime == 0 {
		c.ConnMaxIdleTime = 5 * time.Minute
	}
}

// Database represents a generic database interface
type Database interface {
	// Core database operations
	Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(ctx context.Context, query string, args ...interface{}) *sql.Row
	Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error)

	// Transaction support
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)

	// Connection management
	Connect() error
	Close() error
	Ping(ctx context.Context) error

	// Metadata
	DriverName() string
	ConnectionString() string

	// DB object access (for specific DB operations)
	DB() *sql.DB
}

// database is the concrete implementation of the Database interface
type database struct {
	config     Config
	db         *sql.DB
	driverName string
	dsn        string
}

// NewDatabase creates a new database connection based on the provided configuration
func NewDatabase(config Config) (Database, error) {
	// Set default values for the configuration
	config.SetDefaults()

	var dsn string
	var driverName string

	// Create DSN string based on database type
	switch config.Type {
	case "mysql":
		driverName = "mysql"
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
			config.User, config.Password, config.Host, config.Port, config.Name)
	case "postgres":
		driverName = "postgres"
		dsn = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			config.Host, config.Port, config.User, config.Password, config.Name)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", config.Type)
	}

	return &database{
		config:     config,
		driverName: driverName,
		dsn:        dsn,
	}, nil
}

// Connect establishes a connection to the database
func (d *database) Connect() error {
	db, err := sql.Open(d.driverName, d.dsn)
	if err != nil {
		return fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(d.config.MaxOpenConns)
	db.SetMaxIdleConns(d.config.MaxIdleConns)
	db.SetConnMaxLifetime(d.config.ConnMaxLifetime)
	db.SetConnMaxIdleTime(d.config.ConnMaxIdleTime)

	// Verify connection is working
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			fmt.Printf("Error closing database connection: %v\n", closeErr)
		}
		return fmt.Errorf("failed to ping database: %w", err)
	}

	d.db = db
	logger.Info("Connected to %s database at %s:%d/%s", d.config.Type, d.config.Host, d.config.Port, d.config.Name)

	return nil
}

// Close closes the database connection
func (d *database) Close() error {
	if d.db == nil {
		return nil
	}
	if err := d.db.Close(); err != nil {
		logger.Error("Error closing database connection: %v", err)
		return err
	}
	return nil
}

// Ping checks if the database connection is still alive
func (d *database) Ping(ctx context.Context) error {
	if d.db == nil {
		return ErrNoDatabase
	}
	return d.db.PingContext(ctx)
}

// Query executes a query that returns rows
func (d *database) Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	if d.db == nil {
		return nil, ErrNoDatabase
	}
	return d.db.QueryContext(ctx, query, args...)
}

// QueryRow executes a query that is expected to return at most one row
func (d *database) QueryRow(ctx context.Context, query string, args ...interface{}) *sql.Row {
	if d.db == nil {
		return nil
	}
	return d.db.QueryRowContext(ctx, query, args...)
}

// Exec executes a query without returning any rows
func (d *database) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if d.db == nil {
		return nil, ErrNoDatabase
	}
	return d.db.ExecContext(ctx, query, args...)
}

// BeginTx starts a transaction
func (d *database) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	if d.db == nil {
		return nil, ErrNoDatabase
	}
	return d.db.BeginTx(ctx, opts)
}

// DB returns the underlying database connection
func (d *database) DB() *sql.DB {
	return d.db
}

// DriverName returns the name of the database driver
func (d *database) DriverName() string {
	return d.driverName
}

// ConnectionString returns the connection string (with password masked)
func (d *database) ConnectionString() string {
	// Return masked DSN (hide password)
	switch d.config.Type {
	case "mysql":
		return fmt.Sprintf("%s:***@tcp(%s:%d)/%s",
			d.config.User, d.config.Host, d.config.Port, d.config.Name)
	case "postgres":
		return fmt.Sprintf("host=%s port=%d user=%s password=*** dbname=%s sslmode=disable",
			d.config.Host, d.config.Port, d.config.User, d.config.Name)
	default:
		return "unknown"
	}
}
