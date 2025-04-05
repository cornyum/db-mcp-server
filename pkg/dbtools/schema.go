package dbtools

import (
	"context"
	"fmt"
	"time"

	"github.com/cornyum/db-mcp-server/pkg/db"
	"github.com/cornyum/db-mcp-server/pkg/logger"
	"github.com/cornyum/db-mcp-server/pkg/tools"
)

// createSchemaExplorerTool creates a tool for exploring database schema
func createSchemaExplorerTool() *tools.Tool {
	return &tools.Tool{
		Name:        "dbSchema",
		Description: "Auto-discover database structure and relationships",
		Category:    "database",
		InputSchema: tools.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"component": map[string]interface{}{
					"type":        "string",
					"description": "Schema component to explore (tables, columns, relationships, or full)",
					"enum":        []string{"tables", "columns", "relationships", "full"},
				},
				"table": map[string]interface{}{
					"type":        "string",
					"description": "Table name to explore (optional, leave empty for all tables)",
				},
				"timeout": map[string]interface{}{
					"type":        "integer",
					"description": "Query timeout in milliseconds (default: 10000)",
				},
				"database": map[string]interface{}{
					"type":        "string",
					"description": "Database ID to use (optional if only one database is configured)",
				},
			},
			Required: []string{"component", "database"},
		},
		Handler: handleSchemaExplorer,
	}
}

// handleSchemaExplorer handles the schema explorer tool execution
func handleSchemaExplorer(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Check if database manager is initialized
	if dbManager == nil {
		return nil, fmt.Errorf("database manager not initialized")
	}

	// Extract parameters
	component, ok := getStringParam(params, "component")
	if !ok {
		return nil, fmt.Errorf("component parameter is required")
	}

	// Get database ID
	databaseID, ok := getStringParam(params, "database")
	if !ok {
		return nil, fmt.Errorf("database parameter is required")
	}

	// Get database instance
	db, err := dbManager.GetDB(databaseID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	// Extract table parameter (optional depending on component)
	table, _ := getStringParam(params, "table")

	// Extract timeout
	timeout := 10000 // Default timeout: 10 seconds
	if timeoutParam, ok := getIntParam(params, "timeout"); ok {
		timeout = timeoutParam
	}

	// Create context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()

	// Use actual database queries based on component type
	switch component {
	case "tables":
		return getTables(timeoutCtx, db)
	case "columns":
		if table == "" {
			return nil, fmt.Errorf("table parameter is required for columns component")
		}
		return getColumns(timeoutCtx, db, table)
	case "relationships":
		return getRelationships(timeoutCtx, db, table)
	case "full":
		return getFullSchema(timeoutCtx, db)
	default:
		return nil, fmt.Errorf("invalid component: %s", component)
	}
}

// getTables retrieves the list of tables in the database
func getTables(ctx context.Context, db db.Database) (interface{}, error) {
	// Get database type from connected database
	dbType := "mysql" // Default to MySQL
	if db.DriverName() == "postgres" {
		dbType = "postgres"
	}

	var query string

	// Use database-specific query
	if dbType == "postgres" {
		query = "SELECT tablename as table_name FROM pg_catalog.pg_tables WHERE schemaname = 'public'"
	} else {
		// MySQL
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()"
	}

	rows, err := db.Query(ctx, query)
	if err != nil {
		// Fallback queries if primary query fails
		if dbType == "postgres" {
			query = "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'"
		} else {
			query = "SHOW TABLES"
		}
		rows, err = db.Query(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("failed to get tables: %w", err)
		}
	}

	defer func() {
		if rows != nil {
			if err := rows.Close(); err != nil {
				logger.Error("error closing rows: %v", err)
			}
		}
	}()

	// Convert rows to maps
	results, err := rowsToMaps(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to process tables: %w", err)
	}

	return map[string]interface{}{
		"tables": results,
	}, nil
}

// getColumns retrieves the columns for a specific table
func getColumns(ctx context.Context, db db.Database, table string) (interface{}, error) {
	// Get database type from connected database
	dbType := "mysql" // Default to MySQL
	if db.DriverName() == "postgres" {
		dbType = "postgres"
	}

	var query string

	// Use database-specific query
	if dbType == "postgres" {
		query = `
			SELECT column_name, data_type, 
				   CASE WHEN is_nullable = 'YES' THEN 'YES' ELSE 'NO' END as is_nullable,
				   column_default
			FROM information_schema.columns 
			WHERE table_name = $1 AND table_schema = 'public'
			ORDER BY ordinal_position
		`
	} else {
		// MySQL
		query = `
			SELECT column_name, data_type, is_nullable, column_default
			FROM information_schema.columns
			WHERE table_name = ? AND table_schema = DATABASE()
			ORDER BY ordinal_position
		`
	}

	rows, err := db.Query(ctx, query, table)
	if err != nil {
		return nil, fmt.Errorf("failed to get columns for table %s: %w", table, err)
	}

	defer func() {
		if rows != nil {
			if err := rows.Close(); err != nil {
				logger.Error("error closing rows: %v", err)
			}
		}
	}()

	// Convert rows to maps
	results, err := rowsToMaps(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to process columns: %w", err)
	}

	return map[string]interface{}{
		"table":   table,
		"columns": results,
	}, nil
}

// getRelationships retrieves the relationships for a table or all tables
func getRelationships(ctx context.Context, db db.Database, table string) (interface{}, error) {
	// Get database type from connected database
	dbType := "mysql" // Default to MySQL
	if db.DriverName() == "postgres" {
		dbType = "postgres"
	}

	var query string
	var args []interface{}

	// Use database-specific query
	if dbType == "postgres" {
		query = `
			SELECT
				tc.table_schema,
				tc.constraint_name,
				tc.table_name,
				kcu.column_name,
				ccu.table_schema AS foreign_table_schema,
				ccu.table_name AS foreign_table_name,
				ccu.column_name AS foreign_column_name
			FROM information_schema.table_constraints AS tc
			JOIN information_schema.key_column_usage AS kcu
				ON tc.constraint_name = kcu.constraint_name
				AND tc.table_schema = kcu.table_schema
			JOIN information_schema.constraint_column_usage AS ccu
				ON ccu.constraint_name = tc.constraint_name
				AND ccu.table_schema = tc.table_schema
			WHERE tc.constraint_type = 'FOREIGN KEY'
				AND tc.table_schema = 'public'
		`

		if table != "" {
			query += " AND tc.table_name = $1"
			args = append(args, table)
		}
	} else {
		// MySQL
		query = `
			SELECT
				tc.table_schema,
				tc.constraint_name,
				tc.table_name,
				kcu.column_name,
				kcu.referenced_table_schema AS foreign_table_schema,
				kcu.referenced_table_name AS foreign_table_name,
				kcu.referenced_column_name AS foreign_column_name
			FROM information_schema.table_constraints AS tc
			JOIN information_schema.key_column_usage AS kcu
				ON tc.constraint_name = kcu.constraint_name
				AND tc.table_schema = kcu.table_schema
			WHERE tc.constraint_type = 'FOREIGN KEY'
				AND tc.table_schema = DATABASE()
		`

		if table != "" {
			query += " AND tc.table_name = ?"
			args = append(args, table)
		}
	}

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get relationships for table %s: %w", table, err)
	}

	defer func() {
		if rows != nil {
			if err := rows.Close(); err != nil {
				logger.Error("error closing rows: %v", err)
			}
		}
	}()

	// Convert rows to maps
	results, err := rowsToMaps(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to process relationships: %w", err)
	}

	return map[string]interface{}{
		"relationships": results,
	}, nil
}

// safeGetMap safely gets a map from an interface value
func safeGetMap(obj interface{}) (map[string]interface{}, error) {
	if obj == nil {
		return nil, fmt.Errorf("nil value cannot be converted to map")
	}

	mapVal, ok := obj.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("value is not a map[string]interface{}: %T", obj)
	}

	return mapVal, nil
}

// safeGetString safely gets a string from a map key
func safeGetString(m map[string]interface{}, key string) (string, error) {
	val, ok := m[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in map", key)
	}

	strVal, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("value for key %q is not a string: %T", key, val)
	}

	return strVal, nil
}

// getFullSchema retrieves the complete database schema
func getFullSchema(ctx context.Context, db db.Database) (interface{}, error) {
	tablesResult, err := getTables(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("failed to get tables: %w", err)
	}

	tablesMap, err := safeGetMap(tablesResult)
	if err != nil {
		return nil, fmt.Errorf("invalid tables result: %w", err)
	}

	tablesSlice, ok := tablesMap["tables"].([]map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid tables data format")
	}

	// For each table, get columns
	fullSchema := make(map[string]interface{})
	for _, tableInfo := range tablesSlice {
		tableName, err := safeGetString(tableInfo, "table_name")
		if err != nil {
			return nil, fmt.Errorf("invalid table info: %w", err)
		}

		columnsResult, columnsErr := getColumns(ctx, db, tableName)
		if columnsErr != nil {
			return nil, fmt.Errorf("failed to get columns for table %s: %w", tableName, columnsErr)
		}
		fullSchema[tableName] = columnsResult
	}

	// Get all relationships
	relationships, relErr := getRelationships(ctx, db, "")
	if relErr != nil {
		return nil, fmt.Errorf("failed to get relationships: %w", relErr)
	}

	relMap, err := safeGetMap(relationships)
	if err != nil {
		return nil, fmt.Errorf("invalid relationships result: %w", err)
	}

	return map[string]interface{}{
		"tables":        tablesSlice,
		"schema":        fullSchema,
		"relationships": relMap["relationships"],
	}, nil
}

// getMockTables returns mock table data
//
//nolint:unused // Mock function for testing/development
func getMockTables() (interface{}, error) {
	tables := []map[string]interface{}{
		{
			"name":                "users",
			"type":                "BASE TABLE",
			"engine":              "InnoDB",
			"estimated_row_count": 1500,
			"create_time":         time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
			"update_time":         time.Now().Add(-2 * 24 * time.Hour).Format(time.RFC3339),
		},
		{
			"name":                "orders",
			"type":                "BASE TABLE",
			"engine":              "InnoDB",
			"estimated_row_count": 8750,
			"create_time":         time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
			"update_time":         time.Now().Add(-1 * 24 * time.Hour).Format(time.RFC3339),
		},
		{
			"name":                "products",
			"type":                "BASE TABLE",
			"engine":              "InnoDB",
			"estimated_row_count": 350,
			"create_time":         time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
			"update_time":         time.Now().Add(-5 * 24 * time.Hour).Format(time.RFC3339),
		},
	}

	return map[string]interface{}{
		"tables": tables,
		"count":  len(tables),
		"type":   "mysql",
	}, nil
}

// getMockColumns returns mock column data for a given table
//
//nolint:unused // Mock function for testing/development
func getMockColumns(table string) (interface{}, error) {
	var columns []map[string]interface{}

	switch table {
	case "users":
		columns = []map[string]interface{}{
			{
				"name":              "id",
				"type":              "int(11)",
				"nullable":          "NO",
				"key":               "PRI",
				"extra":             "auto_increment",
				"default":           nil,
				"max_length":        nil,
				"numeric_precision": 10,
				"numeric_scale":     0,
				"comment":           "User unique identifier",
			},
			{
				"name":              "email",
				"type":              "varchar(255)",
				"nullable":          "NO",
				"key":               "UNI",
				"extra":             "",
				"default":           nil,
				"max_length":        255,
				"numeric_precision": nil,
				"numeric_scale":     nil,
				"comment":           "User email address",
			},
			{
				"name":              "name",
				"type":              "varchar(100)",
				"nullable":          "NO",
				"key":               "",
				"extra":             "",
				"default":           nil,
				"max_length":        100,
				"numeric_precision": nil,
				"numeric_scale":     nil,
				"comment":           "User full name",
			},
			{
				"name":              "created_at",
				"type":              "timestamp",
				"nullable":          "NO",
				"key":               "",
				"extra":             "",
				"default":           "CURRENT_TIMESTAMP",
				"max_length":        nil,
				"numeric_precision": nil,
				"numeric_scale":     nil,
				"comment":           "Creation timestamp",
			},
		}
	case "orders":
		columns = []map[string]interface{}{
			{
				"name":              "id",
				"type":              "int(11)",
				"nullable":          "NO",
				"key":               "PRI",
				"extra":             "auto_increment",
				"default":           nil,
				"max_length":        nil,
				"numeric_precision": 10,
				"numeric_scale":     0,
				"comment":           "Order ID",
			},
			{
				"name":              "user_id",
				"type":              "int(11)",
				"nullable":          "NO",
				"key":               "MUL",
				"extra":             "",
				"default":           nil,
				"max_length":        nil,
				"numeric_precision": 10,
				"numeric_scale":     0,
				"comment":           "User who placed the order",
			},
			{
				"name":              "total_amount",
				"type":              "decimal(10,2)",
				"nullable":          "NO",
				"key":               "",
				"extra":             "",
				"default":           "0.00",
				"max_length":        nil,
				"numeric_precision": 10,
				"numeric_scale":     2,
				"comment":           "Total order amount",
			},
			{
				"name":              "status",
				"type":              "enum('pending','processing','shipped','delivered')",
				"nullable":          "NO",
				"key":               "",
				"extra":             "",
				"default":           "pending",
				"max_length":        nil,
				"numeric_precision": nil,
				"numeric_scale":     nil,
				"comment":           "Order status",
			},
			{
				"name":              "created_at",
				"type":              "timestamp",
				"nullable":          "NO",
				"key":               "",
				"extra":             "",
				"default":           "CURRENT_TIMESTAMP",
				"max_length":        nil,
				"numeric_precision": nil,
				"numeric_scale":     nil,
				"comment":           "Order creation time",
			},
		}
	case "products":
		columns = []map[string]interface{}{
			{
				"name":              "id",
				"type":              "int(11)",
				"nullable":          "NO",
				"key":               "PRI",
				"extra":             "auto_increment",
				"default":           nil,
				"max_length":        nil,
				"numeric_precision": 10,
				"numeric_scale":     0,
				"comment":           "Product ID",
			},
			{
				"name":              "name",
				"type":              "varchar(255)",
				"nullable":          "NO",
				"key":               "",
				"extra":             "",
				"default":           nil,
				"max_length":        255,
				"numeric_precision": nil,
				"numeric_scale":     nil,
				"comment":           "Product name",
			},
			{
				"name":              "price",
				"type":              "decimal(10,2)",
				"nullable":          "NO",
				"key":               "",
				"extra":             "",
				"default":           "0.00",
				"max_length":        nil,
				"numeric_precision": 10,
				"numeric_scale":     2,
				"comment":           "Product price",
			},
			{
				"name":              "created_at",
				"type":              "timestamp",
				"nullable":          "NO",
				"key":               "",
				"extra":             "",
				"default":           "CURRENT_TIMESTAMP",
				"max_length":        nil,
				"numeric_precision": nil,
				"numeric_scale":     nil,
				"comment":           "Product creation time",
			},
		}
	default:
		return nil, fmt.Errorf("table %s not found", table)
	}

	return map[string]interface{}{
		"table":   table,
		"columns": columns,
		"count":   len(columns),
		"type":    "mysql",
	}, nil
}

// getMockRelationships returns mock relationship data for a given table
//
//nolint:unused // Mock function for testing/development
func getMockRelationships(table string) (interface{}, error) {
	relationships := []map[string]interface{}{
		{
			"constraint_name":        "fk_orders_users",
			"table_name":             "orders",
			"column_name":            "user_id",
			"referenced_table_name":  "users",
			"referenced_column_name": "id",
			"update_rule":            "CASCADE",
			"delete_rule":            "RESTRICT",
		},
		{
			"constraint_name":        "fk_order_items_orders",
			"table_name":             "order_items",
			"column_name":            "order_id",
			"referenced_table_name":  "orders",
			"referenced_column_name": "id",
			"update_rule":            "CASCADE",
			"delete_rule":            "CASCADE",
		},
		{
			"constraint_name":        "fk_order_items_products",
			"table_name":             "order_items",
			"column_name":            "product_id",
			"referenced_table_name":  "products",
			"referenced_column_name": "id",
			"update_rule":            "CASCADE",
			"delete_rule":            "RESTRICT",
		},
	}

	// Filter by table if provided
	if table != "" {
		filteredRelationships := make([]map[string]interface{}, 0)
		for _, r := range relationships {
			if r["table_name"] == table || r["referenced_table_name"] == table {
				filteredRelationships = append(filteredRelationships, r)
			}
		}
		relationships = filteredRelationships
	}

	return map[string]interface{}{
		"relationships": relationships,
		"count":         len(relationships),
		"type":          "mysql",
		"table":         table,
	}, nil
}

// getMockFullSchema returns a mock complete database schema
//
//nolint:unused // Mock function for testing/development
func getMockFullSchema() (interface{}, error) {
	tablesResult, err := getMockTables()
	if err != nil {
		return nil, fmt.Errorf("failed to get mock tables: %w", err)
	}

	relationshipsResult, err := getMockRelationships("")
	if err != nil {
		return nil, fmt.Errorf("failed to get mock relationships: %w", err)
	}

	tablesMap, err := safeGetMap(tablesResult)
	if err != nil {
		return nil, fmt.Errorf("invalid tables result: %w", err)
	}

	tablesSlice, ok := tablesMap["tables"].([]map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid tables data format")
	}

	tableDetails := make(map[string]interface{})

	for _, tableInfo := range tablesSlice {
		tableName, err := safeGetString(tableInfo, "name")
		if err != nil {
			return nil, fmt.Errorf("invalid table info: %w", err)
		}

		columnsResult, err := getMockColumns(tableName)
		if err != nil {
			return nil, fmt.Errorf("failed to get mock columns for table %s: %w", tableName, err)
		}

		columnsMap, err := safeGetMap(columnsResult)
		if err != nil {
			return nil, fmt.Errorf("invalid columns result: %w", err)
		}

		tableDetails[tableName] = columnsMap["columns"]
	}

	relMap, err := safeGetMap(relationshipsResult)
	if err != nil {
		return nil, fmt.Errorf("invalid relationships result: %w", err)
	}

	return map[string]interface{}{
		"tables":        tablesMap["tables"],
		"relationships": relMap["relationships"],
		"tableDetails":  tableDetails,
		"type":          "mysql",
	}, nil
}
