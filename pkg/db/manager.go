package db

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/cornyum/db-mcp-server/pkg/logger"
)

// DatabaseConnectionConfig represents a single database connection configuration
type DatabaseConnectionConfig struct {
	ID       string `json:"id"`   // Unique identifier for this connection
	Type     string `json:"type"` // mysql or postgres
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

// MultiDBConfig represents the configuration for multiple database connections
type MultiDBConfig struct {
	Connections []DatabaseConnectionConfig `json:"connections"`
}

// Manager manages multiple database connections
type Manager struct {
	mu          sync.RWMutex
	connections map[string]Database
	configs     map[string]DatabaseConnectionConfig
}

// NewDBManager creates a new database manager
func NewDBManager() *Manager {
	return &Manager{
		connections: make(map[string]Database),
		configs:     make(map[string]DatabaseConnectionConfig),
	}
}

// LoadConfig loads database configurations from JSON
func (m *Manager) LoadConfig(configJSON []byte) error {
	var config MultiDBConfig
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return fmt.Errorf("failed to parse config JSON: %w", err)
	}

	// Validate and store configurations
	for _, conn := range config.Connections {
		if conn.ID == "" {
			return fmt.Errorf("database connection ID cannot be empty")
		}
		if conn.Type != "mysql" && conn.Type != "postgres" {
			return fmt.Errorf("unsupported database type for connection %s: %s", conn.ID, conn.Type)
		}
		m.configs[conn.ID] = conn
	}

	return nil
}

// Connect establishes connections to all configured databases
func (m *Manager) Connect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var connectErrors []error
	totalConfigs := len(m.configs)
	successfulConnections := 0

	for id, cfg := range m.configs {
		dbConfig := Config{
			Type:     cfg.Type,
			Host:     cfg.Host,
			Port:     cfg.Port,
			User:     cfg.User,
			Password: cfg.Password,
			Name:     cfg.Name,
		}

		// Create and connect to database
		db, err := NewDatabase(dbConfig)
		if err != nil {
			errMsg := fmt.Errorf("failed to create database instance for %s: %w", id, err)
			connectErrors = append(connectErrors, errMsg)
			logger.Error("Error: %v", errMsg)
			continue
		}

		if err := db.Connect(); err != nil {
			errMsg := fmt.Errorf("failed to connect to database %s: %w", id, err)
			connectErrors = append(connectErrors, errMsg)
			logger.Error("Error: %v", errMsg)
			continue
		}

		// Store successful connection
		m.connections[id] = db
		successfulConnections++
	}

	// Report connection status
	if successfulConnections == 0 && len(connectErrors) > 0 {
		return fmt.Errorf("failed to connect to any database: %v", connectErrors)
	}

	if len(connectErrors) > 0 {
		logger.Warn("Warning: Connected to %d/%d databases. %d failed: %v",
			successfulConnections, totalConfigs, len(connectErrors), connectErrors)
	} else {
		logger.Info("Successfully connected to all %d databases", successfulConnections)
	}

	return nil
}

// GetDB returns a database connection by its ID
func (m *Manager) GetDB(id string) (Database, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	db, exists := m.connections[id]
	if !exists {
		return nil, fmt.Errorf("database connection %s not found", id)
	}
	return db, nil
}

// ListDatabases returns a list of all available database connections
func (m *Manager) ListDatabases() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var dbs []string
	for id := range m.connections {
		dbs = append(dbs, id)
	}
	return dbs
}

// Close closes all database connections
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for id, db := range m.connections {
		if err := db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close database %s: %w", id, err))
		}
	}

	m.connections = make(map[string]Database)

	if len(errs) > 0 {
		return fmt.Errorf("errors closing databases: %v", errs)
	}
	return nil
}

// Ping checks if all database connections are alive
func (m *Manager) Ping(ctx context.Context) map[string]error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make(map[string]error)
	for id, db := range m.connections {
		results[id] = db.Ping(ctx)
	}
	return results
}
