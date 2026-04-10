package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// SchemaManager handles schema extraction, caching, and versioning
type SchemaManager struct {
	db            *sql.DB
	metadataCache map[string]*SchemaMetadata
	lastSync      map[string]time.Time
	cacheDuration time.Duration
	mu            sync.RWMutex
}

// SchemaMetadata represents the complete schema information
type SchemaMetadata struct {
	BusinessID    string         `json:"business_id"`
	Version       string         `json:"version"`
	ExtractedAt   time.Time      `json:"extracted_at"`
	Tables        []TableInfo    `json:"tables"`
	Relationships []Relationship `json:"relationships"`
}

// TableInfo represents a database table
type TableInfo struct {
	Name        string       `json:"name"`
	Type        string       `json:"type"` // fact_table, dimension_table
	Description string       `json:"description"`
	Columns     []ColumnInfo `json:"columns"`
}

// ColumnInfo represents a table column
type ColumnInfo struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	Description      string `json:"description,omitempty"`
	IsNullable       bool   `json:"is_nullable"`
	IsPrimaryKey     bool   `json:"is_primary_key"`
	IsForeignKey     bool   `json:"is_foreign_key"`
	ForeignKeyTable  string `json:"foreign_key_table,omitempty"`
	ForeignKeyColumn string `json:"foreign_key_column,omitempty"`
	BusinessTerm     string `json:"business_term,omitempty"`
	AggregationPref  string `json:"aggregation_preference,omitempty"`
}

// Relationship represents a foreign key relationship
type Relationship struct {
	FromTable  string `json:"from_table"`
	FromColumn string `json:"from_column"`
	ToTable    string `json:"to_table"`
	ToColumn   string `json:"to_column"`
	Type       string `json:"type"` // one_to_one, one_to_many, many_to_one
}

// NewSchemaManager creates a new schema manager
func NewSchemaManager(db *sql.DB) *SchemaManager {
	return &SchemaManager{
		db:            db,
		metadataCache: make(map[string]*SchemaMetadata),
		lastSync:      make(map[string]time.Time),
		cacheDuration: 1 * time.Hour,
	}
}

// GetSchema retrieves schema metadata for a business
func (sm *SchemaManager) GetSchema(ctx context.Context, businessID string, forceRefresh bool) (*SchemaMetadata, error) {
	// Check cache first
	sm.mu.RLock()
	if !forceRefresh {
		if cached, exists := sm.metadataCache[businessID]; exists && sm.isFresh(businessID) {
			sm.mu.RUnlock()
			logrus.Debugf("Returning cached schema for business %s", businessID)
			return cached, nil
		}
	}
	sm.mu.RUnlock()

	// Extract fresh metadata
	metadata, err := sm.ExtractSchema(ctx, businessID)
	if err != nil {
		return nil, fmt.Errorf("failed to extract schema: %w", err)
	}

	// Update cache
	sm.mu.Lock()
	sm.metadataCache[businessID] = metadata
	sm.lastSync[businessID] = time.Now()
	sm.mu.Unlock()

	logrus.Infof("Schema extracted and cached for business %s", businessID)
	return metadata, nil
}

// ExtractSchema extracts schema metadata from the database
func (sm *SchemaManager) ExtractSchema(ctx context.Context, businessID string) (*SchemaMetadata, error) {
	metadata := &SchemaMetadata{
		BusinessID:    businessID,
		Version:       "1.0",
		ExtractedAt:   time.Now(),
		Tables:        make([]TableInfo, 0),
		Relationships: make([]Relationship, 0),
	}

	// Get all tables in public schema (excluding system tables)
	tableQuery := `
		SELECT 
			t.table_name,
			COALESCE(obj_description(pgc.oid, 'pg_class'), '') as description
		FROM information_schema.tables t
		JOIN pg_class pgc ON pgc.relname = t.table_name
		WHERE t.table_schema = 'public'
		AND t.table_type = 'BASE TABLE'
		ORDER BY t.table_name
	`

	rows, err := sm.db.QueryContext(ctx, tableQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tableName, description string
		if err := rows.Scan(&tableName, &description); err != nil {
			continue
		}

		tableInfo := TableInfo{
			Name:        tableName,
			Description: description,
			Type:        sm.inferTableType(tableName),
			Columns:     make([]ColumnInfo, 0),
		}

		// Get columns for this table
		columns, err := sm.getColumns(ctx, tableName)
		if err != nil {
			logrus.Warnf("Failed to get columns for table %s: %v", tableName, err)
			continue
		}
		tableInfo.Columns = columns

		metadata.Tables = append(metadata.Tables, tableInfo)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tables: %w", err)
	}

	// Extract relationships
	relationships, err := sm.getRelationships(ctx)
	if err != nil {
		logrus.Warnf("Failed to extract relationships: %v", err)
	} else {
		metadata.Relationships = relationships
	}

	return metadata, nil
}

// getColumns retrieves column information for a table
func (sm *SchemaManager) getColumns(ctx context.Context, tableName string) ([]ColumnInfo, error) {
	query := `
		SELECT 
			c.column_name,
			c.data_type,
			c.is_nullable = 'YES' as is_nullable,
			COALESCE(col_description(pgc.oid, c.ordinal_position), '') as description,
			CASE WHEN pk.column_name IS NOT NULL THEN true ELSE false END as is_primary_key
		FROM information_schema.columns c
		JOIN pg_class pgc ON pgc.relname = c.table_name
		LEFT JOIN (
			SELECT kcu.column_name, kcu.table_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu 
				ON tc.constraint_name = kcu.constraint_name
			WHERE tc.constraint_type = 'PRIMARY KEY'
		) pk ON pk.column_name = c.column_name AND pk.table_name = c.table_name
		WHERE c.table_schema = 'public'
		AND c.table_name = $1
		ORDER BY c.ordinal_position
	`

	rows, err := sm.db.QueryContext(ctx, query, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []ColumnInfo
	for rows.Next() {
		var col ColumnInfo
		var description string
		if err := rows.Scan(&col.Name, &col.Type, &col.IsNullable, &description, &col.IsPrimaryKey); err != nil {
			continue
		}
		col.Description = description
		col.Type = sm.normalizeDataType(col.Type)
		columns = append(columns, col)
	}

	// Get foreign key information
	fkQuery := `
		SELECT
			kcu.column_name,
			ccu.table_name AS foreign_table,
			ccu.column_name AS foreign_column
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu 
			ON tc.constraint_name = kcu.constraint_name
		JOIN information_schema.constraint_column_usage ccu 
			ON ccu.constraint_name = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY'
		AND tc.table_name = $1
	`

	fkRows, err := sm.db.QueryContext(ctx, fkQuery, tableName)
	if err == nil {
		defer fkRows.Close()
		for fkRows.Next() {
			var colName, foreignTable, foreignColumn string
			if err := fkRows.Scan(&colName, &foreignTable, &foreignColumn); err != nil {
				continue
			}
			// Update column with FK info
			for i := range columns {
				if columns[i].Name == colName {
					columns[i].IsForeignKey = true
					columns[i].ForeignKeyTable = foreignTable
					columns[i].ForeignKeyColumn = foreignColumn
					break
				}
			}
		}
	}

	return columns, rows.Err()
}

// getRelationships extracts foreign key relationships
func (sm *SchemaManager) getRelationships(ctx context.Context) ([]Relationship, error) {
	query := `
		SELECT
			tc.table_name as from_table,
			kcu.column_name as from_column,
			ccu.table_name as to_table,
			ccu.column_name as to_column
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu 
			ON tc.constraint_name = kcu.constraint_name
		JOIN information_schema.constraint_column_usage ccu 
			ON ccu.constraint_name = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY'
		AND tc.table_schema = 'public'
	`

	rows, err := sm.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relationships []Relationship
	for rows.Next() {
		var rel Relationship
		if err := rows.Scan(&rel.FromTable, &rel.FromColumn, &rel.ToTable, &rel.ToColumn); err != nil {
			continue
		}
		rel.Type = "many_to_one"
		relationships = append(relationships, rel)
	}

	return relationships, rows.Err()
}

// inferTableType determines if a table is a fact or dimension table
func (sm *SchemaManager) inferTableType(tableName string) string {
	lowerName := strings.ToLower(tableName)
	if strings.HasPrefix(lowerName, "dim_") {
		return "dimension_table"
	}
	if strings.HasPrefix(lowerName, "fact_") {
		return "fact_table"
	}
	// Heuristic: tables with date columns and foreign keys are likely fact tables
	return "table"
}

// normalizeDataType normalizes PostgreSQL data types
func (sm *SchemaManager) normalizeDataType(pgType string) string {
	switch pgType {
	case "character varying", "varchar", "character", "char", "text":
		return "string"
	case "integer", "bigint", "smallint", "serial", "bigserial":
		return "integer"
	case "numeric", "decimal", "real", "double precision":
		return "number"
	case "timestamp without time zone", "timestamp with time zone", "date", "time":
		return "datetime"
	case "boolean":
		return "boolean"
	default:
		return pgType
	}
}

// isFresh checks if cached schema is still fresh
func (sm *SchemaManager) isFresh(businessID string) bool {
	lastSync, exists := sm.lastSync[businessID]
	if !exists {
		return false
	}
	return time.Since(lastSync) < sm.cacheDuration
}

// ToPromptFormat converts schema to a format suitable for LLM prompts
func (sm *SchemaManager) ToPromptFormat(metadata *SchemaMetadata) string {
	var sb strings.Builder

	sb.WriteString("Database Schema:\n\n")

	for _, table := range metadata.Tables {
		sb.WriteString(fmt.Sprintf("Table: %s", table.Name))
		if table.Description != "" {
			sb.WriteString(fmt.Sprintf(" - %s", table.Description))
		}
		sb.WriteString("\n")

		for _, col := range table.Columns {
			sb.WriteString(fmt.Sprintf("  • %s (%s)", col.Name, col.Type))
			if col.Description != "" {
				sb.WriteString(fmt.Sprintf(": %s", col.Description))
			}
			if col.IsPrimaryKey {
				sb.WriteString(" [PK]")
			}
			if col.IsForeignKey {
				sb.WriteString(fmt.Sprintf(" → %s.%s", col.ForeignKeyTable, col.ForeignKeyColumn))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	if len(metadata.Relationships) > 0 {
		sb.WriteString("Relationships:\n")
		for _, rel := range metadata.Relationships {
			sb.WriteString(fmt.Sprintf("  • %s.%s → %s.%s\n",
				rel.FromTable, rel.FromColumn, rel.ToTable, rel.ToColumn))
		}
	}

	return sb.String()
}

// InvalidateCache clears the cache for a business
func (sm *SchemaManager) InvalidateCache(businessID string) {
	sm.mu.Lock()
	delete(sm.metadataCache, businessID)
	delete(sm.lastSync, businessID)
	sm.mu.Unlock()
	logrus.Infof("Cache invalidated for business %s", businessID)
}
