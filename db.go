package main

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
	readonly bool
}

func OpenDB(path string, readonly bool) (*DB, error) {
	var flags string
	if readonly {
		flags = "&mode=rwc"
	} else {
		flags = ""
	}
	d, err := sql.Open("sqlite", path+"?_busy_timeout=1000"+flags)
	if err != nil {
		return nil, err
	}
	
	// For readonly mode, we still need to create tables if they don't exist
	// but we'll handle write rejection at the API level
	db := &DB{db: d, readonly: readonly}
	err = db.initSchema()
	if err != nil {
		return nil, err
	}
	return db, nil
}

func (d *DB) initSchema() error {
	sqls := []string{
		`
		CREATE TABLE IF NOT EXISTS items (
			schema TEXT NOT NULL,
			entry_id TEXT NOT NULL,
			attribute TEXT NOT NULL,
			value TEXT NOT NULL,
			UNIQUE(entry_id, attribute)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_schema ON items (schema)`,
		`CREATE INDEX IF NOT EXISTS idx_entry_id ON items (entry_id)`,
		`CREATE INDEX IF NOT EXISTS idx_attribute ON items (attribute)`,
		`CREATE INDEX IF NOT EXISTS idx_value ON items (value)`,
	}
	for _, sql := range sqls {
		_, err := d.db.Exec(sql)
		if err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}
	return nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) CheckWrite() error {
	if d.readonly {
		return fmt.Errorf("read-only mode")
	}
	return nil
}

func (d *DB) GetSchemas() ([]string, error) {
	rows, err := d.db.Query("SELECT DISTINCT schema FROM items WHERE schema != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var schemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		schemas = append(schemas, s)
	}
	return schemas, nil
}

func (d *DB) GetEntries(schema, search string, filters map[string][]string, sortOrder []string) ([]Entry, error) {
	// Build query to get all attributes for entries matching criteria
	query := `
		SELECT schema, entry_id, attribute, value FROM items
		WHERE schema = ?
	`
	args := []interface{}{schema}
	
	// Handle search
	if search != "" {
		query += ` AND entry_id IN (
			SELECT DISTINCT entry_id FROM items
			WHERE schema = ? AND value LIKE ?
		)`
		args = append(args, schema, "%"+search+"%")
	}
	
	// Handle filters
	for attr, vals := range filters {
		if len(vals) > 0 {
			valPlaceholders := strings.Repeat(", ?", len(vals)-1)
			placeholders := "(?" + valPlaceholders + ")"
			query += " AND entry_id IN ("
			query += "SELECT entry_id FROM items WHERE schema = ? AND attribute = ? AND value IN "
			query += placeholders
			query += ")"
			args = append(args, schema, attr)
			for _, val := range vals {
				// cannot use variadic due to slice type mismatch
				args = append(args, val)
			}
		}
	}
	
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	// Group by entry_id
	entriesMap := make(map[string]*Entry)
	var entryOrder []string
	
	for rows.Next() {
		var s, eid, attr, val string
		if err := rows.Scan(&s, &eid, &attr, &val); err != nil {
			return nil, err
		}
		
		if _, ok := entriesMap[eid]; !ok {
			entriesMap[eid] = &Entry{
				Schema:  s,
				EntryID: eid,
				Attrs:   make(map[string]string),
			}
			entryOrder = append(entryOrder, eid)
		}
		entriesMap[eid].Attrs[attr] = val
	}
	
	entries := make([]Entry, 0, len(entryOrder))
	for _, eid := range entryOrder {
		entries = append(entries, *entriesMap[eid])
	}
	
	// Sort entries based on sortOrder
	if len(sortOrder) > 0 {
		sort.SliceStable(entries, func(i, j int) bool {
			for _, attr := range sortOrder {
				valI := entries[i].Attrs[attr]
				valJ := entries[j].Attrs[attr]
				if valI != valJ {
					return valI < valJ
				}
			}
			return false
		})
	}
	
	return entries, nil
}

func (d *DB) GetDistinctValues(schema string) (map[string][]string, error) {
	rows, err := d.db.Query(
		`SELECT attribute, value FROM items
		 WHERE schema = ?
		 GROUP BY value
		 ORDER BY CAST(value AS INTEGER), value`,
		schema,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	// initialize as slice with length 0 to ensure proper
	// JSON marshalling:
	values := make(map[string][]string, 0)
	for rows.Next() {
		var a string
		var v string
		if err := rows.Scan(&a, &v); err != nil {
			return nil, err
		}
		values[a] = append(values[a], v)
	}
	return values, nil
}

func (d *DB) GetAutocomplete(schema, attribute, fragment string) ([]string, error) {
	rows, err := d.db.Query(
		`SELECT DISTINCT value FROM items
		 WHERE schema = ? AND attribute = ? AND value LIKE ?
		 GROUP BY value
		 ORDER BY COUNT(value) DESC
		 LIMIT 20`,
		schema, attribute, "%"+fragment+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, nil
}

func (d *DB) AddEntry(schema, entryID string, attrs map[string]string) error {
	if err := d.CheckWrite(); err != nil {
		return err
	}
	
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO items (schema, entry_id, attribute, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	
	for attr, val := range attrs {
		if val != "" {
			_, err := stmt.Exec(schema, entryID, attr, val)
			if err != nil {
				return err
			}
		}
	}
	
	return tx.Commit()
}

func (d *DB) UpdateEntry(schema, entryID string, attrs map[string]string) error {
	if err := d.CheckWrite(); err != nil {
		return err
	}
	
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	
	// Delete existing attributes for this entry
	_, err = tx.Exec(`DELETE FROM items WHERE schema = ? AND entry_id = ?`, schema, entryID)
	if err != nil {
		return err
	}
	
	stmt, err := tx.Prepare(`INSERT INTO items (schema, entry_id, attribute, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	
	for attr, val := range attrs {
		if val != "" {
			_, err := stmt.Exec(schema, entryID, attr, val)
			if err != nil {
				return err
			}
		}
	}
	
	return tx.Commit()
}

func (d *DB) DeleteEntry(schema, entryID string) error {
	if err := d.CheckWrite(); err != nil {
		return err
	}
	
	_, err := d.db.Exec(`DELETE FROM items WHERE schema = ? AND entry_id = ?`, schema, entryID)
	return err
}
