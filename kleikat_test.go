package main

import (
	"encoding/csv"
	"os"
	"strings"
	"testing"
)

func setupTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "kleikat-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	dbPath := tmpDir + "/test.db"
	db, err := OpenDB(dbPath, false)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("open test db: %v", err)
	}
	return db, tmpDir
}

func TestDBInitSchema(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer func() { db.Close(); os.RemoveAll(tmpDir) }()

	// Verify table exists by inserting and querying
	err := db.AddEntry("clothing", "test1", map[string]string{
		"type":    "Hemd",
		"color":   "Blau",
		"size":    "M",
	})
	if err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	entries, err := db.GetEntries("clothing", "", nil)
	if err != nil {
		t.Fatalf("GetEntries failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Attrs["type"] != "Hemd" {
		t.Fatalf("expected type=Hemd, got %s", entries[0].Attrs["type"])
	}
}

func TestDBUniqueConstraint(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer func() { db.Close(); os.RemoveAll(tmpDir) }()

	// Add two entries with different attributes for same entry_id
	err := db.AddEntry("clothing", "dup1", map[string]string{
		"type":  "Jacke",
		"color": "Rot",
	})
	if err != nil {
		t.Fatalf("first AddEntry failed: %v", err)
	}

	err = db.AddEntry("clothing", "dup1", map[string]string{
		"size": "L",
	})
	if err != nil {
		t.Fatalf("second AddEntry (same entry_id) failed: %v", err)
	}

	entries, err := db.GetEntries("clothing", "", nil)
	if err != nil {
		t.Fatalf("GetEntries failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (merged), got %d", len(entries))
	}
	if entries[0].Attrs["size"] != "L" {
		t.Fatalf("expected size=L, got %s", entries[0].Attrs["size"])
	}
}

func TestDBSearch(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer func() { db.Close(); os.RemoveAll(tmpDir) }()

	db.AddEntry("clothing", "a1", map[string]string{"type": "Hemd", "color": "Blau"})
	db.AddEntry("clothing", "a2", map[string]string{"type": "Jacke", "color": "Blau"})
	db.AddEntry("clothing", "a3", map[string]string{"type": "Hose", "color": "Schwarz"})

	// Search by type
	entries, err := db.GetEntries("clothing", "Hemd", nil)
	if err != nil {
		t.Fatalf("GetEntries with search failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for 'Hemd', got %d", len(entries))
	}

	// Search by color
	entries, err = db.GetEntries("clothing", "Schwarz", nil)
	if err != nil {
		t.Fatalf("GetEntries with search failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for 'Schwarz', got %d", len(entries))
	}
}

func TestDBFilters(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer func() { db.Close(); os.RemoveAll(tmpDir) }()

	db.AddEntry("clothing", "f1", map[string]string{"type": "Hemd", "color": "Blau", "size": "M"})
	db.AddEntry("clothing", "f2", map[string]string{"type": "Hemd", "color": "Rot", "size": "L"})
	db.AddEntry("clothing", "f3", map[string]string{"type": "Jacke", "color": "Blau", "size": "M"})

	// Filter by color
	filters := map[string][]string{"color": {"Blau"}}
	entries, err := db.GetEntries("clothing", "", filters)
	if err != nil {
		t.Fatalf("GetEntries with filters failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries for color=Blau, got %d", len(entries))
	}
}

func TestDBDistinctValues(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer func() { db.Close(); os.RemoveAll(tmpDir) }()

	db.AddEntry("clothing", "d1", map[string]string{"color": "Blau"})
	db.AddEntry("clothing", "d2", map[string]string{"color": "Rot"})
	db.AddEntry("clothing", "d3", map[string]string{"color": "Blau"})

	vals, err := db.GetDistinctValues("clothing")
	if err != nil {
		t.Fatalf("GetDistinctValues failed: %v", err)
	}
	colorVals, ok := vals["color"]
	if !ok {
		t.Fatalf("expected 'color' attribute in result")
	}
	if len(colorVals) != 2 {
		t.Fatalf("expected 2 distinct color values, got %d", len(colorVals))
	}
	if colorVals[0] != "Blau" {
		t.Fatalf("expected first value 'Blau' (most common), got %q", colorVals[0])
	}
}

func TestDBAutocomplete(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer func() { db.Close(); os.RemoveAll(tmpDir) }()

	db.AddEntry("clothing", "ac1", map[string]string{"type": "Hemd"})
	db.AddEntry("clothing", "ac2", map[string]string{"type": "Hemdpullover"})
	db.AddEntry("clothing", "ac3", map[string]string{"type": "Jacke"})

	vals, err := db.GetAutocomplete("clothing", "type", "Hem")
	if err != nil {
		t.Fatalf("GetAutocomplete failed: %v", err)
	}
	if len(vals) != 2 {
		t.Fatalf("expected 2 autocomplete results, got %d", len(vals))
	}
}

func TestDBUpdateEntry(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer func() { db.Close(); os.RemoveAll(tmpDir) }()

	db.AddEntry("clothing", "u1", map[string]string{"type": "Hemd", "color": "Blau"})

	err := db.UpdateEntry("clothing", "u1", map[string]string{"color": "Rot", "size": "M"})
	if err != nil {
		t.Fatalf("UpdateEntry failed: %v", err)
	}

	entries, err := db.GetEntries("clothing", "", nil)
	if err != nil {
		t.Fatalf("GetEntries failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Attrs["color"] != "Rot" {
		t.Fatalf("expected color=Rot, got %s", entries[0].Attrs["color"])
	}
	if entries[0].Attrs["size"] != "M" {
		t.Fatalf("expected size=M, got %s", entries[0].Attrs["size"])
	}
	if _, hasType := entries[0].Attrs["type"]; hasType {
		t.Fatalf("expected type to be removed, but it exists")
	}
}

func TestDBDeleteEntry(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer func() { db.Close(); os.RemoveAll(tmpDir) }()

	db.AddEntry("clothing", "del1", map[string]string{"type": "Hose"})
	db.AddEntry("clothing", "del2", map[string]string{"type": "Jacke"})

	err := db.DeleteEntry("clothing", "del1")
	if err != nil {
		t.Fatalf("DeleteEntry failed: %v", err)
	}

	entries, err := db.GetEntries("clothing", "", nil)
	if err != nil {
		t.Fatalf("GetEntries failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after delete, got %d", len(entries))
	}
	if entries[0].EntryID != "del2" {
		t.Fatalf("expected remaining entry del2, got %s", entries[0].EntryID)
	}
}

func TestDBReadOnly(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)

	// Write first
	err := db.AddEntry("clothing", "ro1", map[string]string{"type": "Test"})
	if err != nil {
		t.Fatalf("initial AddEntry failed: %v", err)
	}
	db.Close()

	// Open readonly
	dbRO, err := OpenDB(tmpDir+"/test.db", true)
	if err != nil {
		t.Fatalf("OpenDB readonly failed: %v", err)
	}
	defer dbRO.Close()

	err = dbRO.CheckWrite()
	if err == nil {
		t.Fatal("expected error from CheckWrite in readonly mode, got nil")
	}

	// Read should still work
	entries, err := dbRO.GetEntries("clothing", "", nil)
	if err != nil {
		t.Fatalf("GetEntries in readonly mode failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry in readonly mode, got %d", len(entries))
	}
}

func TestConfigLoad(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kleikat-config-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := tmpDir + "/config.yaml"
	configData := `tokens:
  read: "test-read-token"
  read_write: "test-readwrite-token"
`
	if err := os.WriteFile(configPath, []byte(configData), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Tokens.Read != "test-read-token" {
		t.Fatalf("expected read token 'test-read-token', got %q", cfg.Tokens.Read)
	}
	if cfg.Tokens.ReadWrite != "test-readwrite-token" {
		t.Fatalf("expected read_write token 'test-readwrite-token', got %q", cfg.Tokens.ReadWrite)
	}
}

func TestConfigLoadWithSchemes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kleikat-schemes-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := tmpDir + "/config.yaml"
	schemasPath := tmpDir + "/schemas.yaml"

	schemasData := `clothing:
  title: Kleidung
  attribute_titles:
    - [type, Art, Arten]
    - [color, Farbe, Farben]
`
	if err := os.WriteFile(schemasPath, []byte(schemasData), 0644); err != nil {
		t.Fatalf("write schemas: %v", err)
	}

	configData := `tokens:
  read: "token1"
  read_write: "token2"
`
	if err := os.WriteFile(configPath, []byte(configData), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Temporarily change working directory
	origCwd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origCwd)

	cfg, err := LoadConfig("config.yaml")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Schemas == nil {
		t.Fatal("expected schemas to be loaded from schemas.yaml")
	}
	if _, ok := cfg.Schemas["clothing"]; !ok {
		t.Fatal("expected 'clothing' scheme to be present")
	}
	if cfg.Schemas["clothing"].Title != "Kleidung" {
		t.Fatalf("expected title 'Kleidung', got %q", cfg.Schemas["clothing"].Title)
	}
	if cfg.Schemas["clothing"].AttributeTitles == nil {
		t.Fatal("expected attribute_titles to be loaded")
	}
	// Find the 'type' attribute in the slice and verify its titles
	foundType := false
	for _, attr := range cfg.Schemas["clothing"].AttributeTitles {
		if len(attr) >= 2 && attr[0] == "type" {
			if attr[1] != "Art" {
				t.Fatalf("expected type title 'Art', got %q", attr[1])
			}
			foundType = true
			break
		}
	}
	if !foundType {
		t.Fatal("expected 'type' attribute in attribute_titles")
	}
}

func TestCSVImport(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kleikat-csv-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create config.yaml with embedded schemas
	configData := `tokens:
  read: "token1"
  read_write: "token2"
schemas:
  shoes:
    title: Schuhe
    attribute_titles:
      - [type, Art, Arten]
      - [size, Größe, Größen]
      - [color, Farbe, Farben]
`
	if err := os.WriteFile(tmpDir+"/config.yaml", []byte(configData), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Create CSV file
	csvData := `type,size,color
Stiefel,42, Schwarz
Sneaker,40, Weiß
Sandale,38, Braun
`
	csvPath := tmpDir + "/shoes.csv"
	if err := os.WriteFile(csvPath, []byte(csvData), 0644); err != nil {
		t.Fatalf("write CSV: %v", err)
	}

	// Open DB
	db, err := OpenDB(tmpDir+"/test.db", false)
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}
	defer db.Close()

	// Load config
	cfg, err := LoadConfig(tmpDir + "/config.yaml")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Import
	reader := csv.NewReader(strings.NewReader(csvData))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}

	count := 0
	headers := records[0]
	for rowIdx := 1; rowIdx < len(records); rowIdx++ {
		row := records[rowIdx]
		attrs := make(map[string]string)
		// Build a map of known attribute names from the scheme
		knownAttrs := make(map[string]bool)
		for _, attr := range cfg.Schemas["shoes"].AttributeTitles {
			if len(attr) > 0 {
				knownAttrs[strings.ToLower(attr[0])] = true
			}
		}
		for colIdx, header := range headers {
			header = strings.TrimSpace(header)
			if colIdx >= len(row) {
				break
			}
			value := strings.TrimSpace(row[colIdx])
			if value == "" {
				continue
			}
			for _, attr := range cfg.Schemas["shoes"].AttributeTitles {
				if len(attr) >= 2 && strings.EqualFold(header, attr[0]) {
					attrs[attr[0]] = value
					break
				}
			}
		}
		if len(attrs) == 0 {
			continue
		}
		entryID := generateID()
		if err := db.AddEntry("shoes", entryID, attrs); err != nil {
			t.Fatalf("AddEntry failed: %v", err)
		}
		count++
	}

	if count != 3 {
		t.Fatalf("expected 3 imported entries, got %d", count)
	}

	// Verify
	entries, err := db.GetEntries("shoes", "", nil)
	if err != nil {
		t.Fatalf("GetEntries failed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries in DB, got %d", len(entries))
	}
}

func TestGenerateID(t *testing.T) {
	id1 := generateID()
	id2 := generateID()
	if id1 == id2 {
		t.Fatal("expected different IDs, got same value")
	}
	if len(id1) != 16 {
		t.Fatalf("expected ID length 16, got %d", len(id1))
	}
}
