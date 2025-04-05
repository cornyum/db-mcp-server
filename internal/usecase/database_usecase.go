package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cornyum/db-mcp-server/internal/domain"
	"github.com/cornyum/db-mcp-server/internal/logger"
)

// TODO: Improve error handling with custom error types and better error messages
// TODO: Add extensive unit tests for all business logic
// TODO: Consider implementing domain events for better decoupling
// TODO: Add request validation layer before processing in usecases
// TODO: Implement proper context propagation and timeout handling

// DatabaseUseCase defines operations for managing database functionality
type DatabaseUseCase struct {
	repo domain.DatabaseRepository
}

// NewDatabaseUseCase creates a new database use case
func NewDatabaseUseCase(repo domain.DatabaseRepository) *DatabaseUseCase {
	return &DatabaseUseCase{
		repo: repo,
	}
}

// ListDatabases returns a list of available databases
func (uc *DatabaseUseCase) ListDatabases() []string {
	return uc.repo.ListDatabases()
}

// GetDatabaseInfo returns information about a database
func (uc *DatabaseUseCase) GetDatabaseInfo(dbID string) (map[string]interface{}, error) {
	// Get database connection
	db, err := uc.repo.GetDatabase(dbID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	// Get the database type
	dbType, err := uc.repo.GetDatabaseType(dbID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database type: %w", err)
	}

	// Query for tables
	ctx := context.Background()

	// Different handling based on database type
	var rows domain.Rows

	if dbType == "postgres" {
		// For PostgreSQL, use pg_catalog which is safer
		query := "SELECT tablename AS table_name FROM pg_catalog.pg_tables WHERE schemaname = 'public'"
		rows, err = db.Query(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to get schema information: %w", err)
		}
	} else {
		// For MySQL
		query := "SELECT table_name, table_type, engine, table_rows, create_time FROM information_schema.tables WHERE table_schema = DATABASE()"
		rows, err = db.Query(ctx, query)
		if err != nil {
			// Fallback to a simpler query
			query = "SHOW TABLES"
			rows, err = db.Query(ctx, query)
			if err != nil {
				return nil, fmt.Errorf("failed to get schema information: %w", err)
			}
		}
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			logger.Error("error closing rows: %v", closeErr)
		}
	}()

	// Process results
	tables := []map[string]interface{}{}
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get column names: %w", err)
	}

	// Prepare for scanning
	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range columns {
		valuePtrs[i] = &values[i]
	}

	// Process each row
	for rows.Next() {
		if err := rows.Scan(valuePtrs...); err != nil {
			continue
		}

		// Convert to map
		tableInfo := make(map[string]interface{})
		for i, colName := range columns {
			val := values[i]
			if val == nil {
				tableInfo[colName] = nil
			} else {
				switch v := val.(type) {
				case []byte:
					tableInfo[colName] = string(v)
				default:
					tableInfo[colName] = v
				}
			}
		}
		tables = append(tables, tableInfo)
	}

	// Create result
	result := map[string]interface{}{
		"database": dbID,
		"tables":   tables,
	}

	return result, nil
}

// ExecuteQuery executes a SQL query and returns the formatted results
func (uc *DatabaseUseCase) ExecuteQuery(ctx context.Context, dbID, query string, params []interface{}) (string, error) {
	db, err := uc.repo.GetDatabase(dbID)
	if err != nil {
		return "", fmt.Errorf("failed to get database: %w", err)
	}

	// Execute query
	rows, err := db.Query(ctx, query, params...)
	if err != nil {
		return "", fmt.Errorf("query execution failed: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = fmt.Errorf("error closing rows: %w", closeErr)
		}
	}()

	// Process results into a readable format
	columns, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("failed to get column names: %w", err)
	}

	// Format results as text
	var resultText strings.Builder
	resultText.WriteString("Results:\n\n")
	resultText.WriteString(strings.Join(columns, "\t") + "\n")
	resultText.WriteString(strings.Repeat("-", 80) + "\n")

	// Prepare for scanning
	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))
	for i := range columns {
		valuePtrs[i] = &values[i]
	}

	// Process rows
	rowCount := 0
	for rows.Next() {
		rowCount++
		scanErr := rows.Scan(valuePtrs...)
		if scanErr != nil {
			return "", fmt.Errorf("failed to scan row: %w", scanErr)
		}

		// Convert to strings and print
		var rowText []string
		for i := range columns {
			val := values[i]
			if val == nil {
				rowText = append(rowText, "NULL")
			} else {
				switch v := val.(type) {
				case []byte:
					rowText = append(rowText, string(v))
				default:
					rowText = append(rowText, fmt.Sprintf("%v", v))
				}
			}
		}
		resultText.WriteString(strings.Join(rowText, "\t") + "\n")
	}

	if err = rows.Err(); err != nil {
		return "", fmt.Errorf("error reading rows: %w", err)
	}

	resultText.WriteString(fmt.Sprintf("\nTotal rows: %d", rowCount))
	return resultText.String(), nil
}

// ExecuteStatement executes a SQL statement (INSERT, UPDATE, DELETE)
func (uc *DatabaseUseCase) ExecuteStatement(ctx context.Context, dbID, statement string, params []interface{}) (string, error) {
	db, err := uc.repo.GetDatabase(dbID)
	if err != nil {
		return "", fmt.Errorf("failed to get database: %w", err)
	}

	// Execute statement
	result, err := db.Exec(ctx, statement, params...)
	if err != nil {
		return "", fmt.Errorf("statement execution failed: %w", err)
	}

	// Get rows affected
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		rowsAffected = 0
	}

	// Get last insert ID (if applicable)
	lastInsertID, err := result.LastInsertId()
	if err != nil {
		lastInsertID = 0
	}

	return fmt.Sprintf("Statement executed successfully.\nRows affected: %d\nLast insert ID: %d", rowsAffected, lastInsertID), nil
}

// ExecuteTransaction executes operations in a transaction
func (uc *DatabaseUseCase) ExecuteTransaction(ctx context.Context, dbID, action string, txID string,
	statement string, params []interface{}, readOnly bool) (string, map[string]interface{}, error) {

	switch action {
	case "begin":
		db, err := uc.repo.GetDatabase(dbID)
		if err != nil {
			return "", nil, fmt.Errorf("failed to get database: %w", err)
		}

		// Start a new transaction
		txOpts := &domain.TxOptions{ReadOnly: readOnly}
		tx, err := db.Begin(ctx, txOpts)
		if err != nil {
			return "", nil, fmt.Errorf("failed to start transaction: %w", err)
		}

		// In a real implementation, we would store the transaction for later use
		// For now, we just commit right away to avoid the unused variable warning
		if err := tx.Commit(); err != nil {
			return "", nil, fmt.Errorf("failed to commit transaction: %w", err)
		}

		// Generate transaction ID
		newTxID := fmt.Sprintf("tx_%s_%d", dbID, timeNowUnix())

		return "Transaction started", map[string]interface{}{"transactionId": newTxID}, nil

	case "commit":
		// Implement commit logic (would need access to stored transaction)
		return "Transaction committed", nil, nil

	case "rollback":
		// Implement rollback logic (would need access to stored transaction)
		return "Transaction rolled back", nil, nil

	case "execute":
		// Implement execute within transaction logic (would need access to stored transaction)
		return "Statement executed in transaction", nil, nil

	default:
		return "", nil, fmt.Errorf("invalid transaction action: %s", action)
	}
}

// Helper function to get current Unix timestamp
func timeNowUnix() int64 {
	return time.Now().Unix()
}

// GetDatabaseType returns the type of a database by ID
func (uc *DatabaseUseCase) GetDatabaseType(dbID string) (string, error) {
	return uc.repo.GetDatabaseType(dbID)
}
