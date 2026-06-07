package main

import (
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

const FILTER_SEP = "^"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "import" {
		runImport(os.Args[2:])
		return
	}

	port := flag.Int("port", 9000, "Port to listen on")
	flag.Parse()

	cfg, err := LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Load config: %v", err)
	}

	db, err := OpenDB("kleikat.db", false)
	if err != nil {
		log.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	// Serve static files
	fs := http.FileServer(http.Dir("assets"))
	http.Handle("/", fs)

	// API routes
	http.HandleFunc("/api/", handleAPI(db, cfg))

	log.Printf("KleiKat starting on :%d", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}

func runImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	schema := fs.String("schema", "", "Schema name (e.g. shoes, clothing)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s import -schema <name> <file.csv>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Import a CSV file into the KleiKat database.\n")
		fmt.Fprintf(os.Stderr, "The CSV must have headers matching the attribute names (e.g. size, color, material).\n")
	}
	fs.Parse(args)

	if *schema == "" || fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	csvPath := fs.Arg(0)
	data, err := os.ReadFile(csvPath)
	if err != nil {
		log.Fatalf("Read CSV: %v", err)
	}

	cfg, err := LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Load config: %v", err)
	}

	// Verify schema exists
	if _, ok := cfg.Schemas[*schema]; !ok {
		log.Fatalf("Unknown schema: %s", *schema)
	}

	db, err := OpenDB("kleikat.db", false)
	if err != nil {
		log.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	reader := csv.NewReader(strings.NewReader(string(data)))
	reader.FieldsPerRecord = -1 // variable number of fields

	records, err := reader.ReadAll()
	if err != nil {
		log.Fatalf("Parse CSV: %v", err)
	}

	if len(records) < 1 {
		log.Fatal("CSV is empty")
	}

	headers := records[0]
	// Trim whitespace and lowercase for matching
	for i := range headers {
		headers[i] = strings.TrimSpace(headers[i])
	}

	// Get scheme attributes to validate headers
	schemeAttrs := cfg.Schemas[*schema].AttributeTitles

	count := 0
	for rowIdx := 1; rowIdx < len(records); rowIdx++ {
		row := records[rowIdx]
		attrs := make(map[string]string)
		for colIdx, header := range headers {
			if colIdx >= len(row) {
				break
			}
			value := strings.TrimSpace(row[colIdx])
			if value == "" {
				continue
			}
			// Match header to schema attribute (case-insensitive)
			matched := false
			for _, rawTitles := range schemeAttrs {
				attrName := rawTitles[0]
				titles := rawTitles[1:]
				if strings.EqualFold(header, attrName) {
					attrs[attrName] = value
					matched = true
					break
				}
				for _, title := range titles {
					if strings.EqualFold(header, title) {
						attrs[attrName] = value
						matched = true
						break
					}
				}
				if matched {
					break
				}
			}
			if !matched {
				log.Printf("Warning: column %q not found in schema %q, skipping", header, *schema)
			}
		}

		if len(attrs) == 0 {
			continue
		}

		entryID := generateID()
		if err := db.AddEntry(*schema, entryID, attrs); err != nil {
			log.Printf("Error adding row %d: %v", rowIdx, err)
			continue
		}
		count++
	}

	log.Printf("Imported %d entries into schema %q from %s", count, *schema, csvPath)
}

func handleAPI(db *DB, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/api/")
		parts := strings.Split(path, "/")

		// /api/schemas (public - no token required)
		if parts[0] == "schemas" {
			schemaInfo := make(map[string]map[string]interface{})
			for name, scheme := range cfg.Schemas {
				schemaInfo[name] = map[string]interface{}{
					"title":            scheme.Title,
					"attribute_titles": scheme.AttributeTitles,
				}
			}
			writeJSON(w, schemaInfo)
			return
		}

		// All other routes require authentication
		token := r.Header.Get("X-Token")
		if token == "" {
			token = r.URL.Query().Get("token")
		}

		if token == "" {
			writeError(w, "missing token", http.StatusForbidden)
			return
		}

		readOnly := true
		tokenParts := strings.Split(token, ".")
		if len(tokenParts) < 1 || tokenParts[0] != cfg.Tokens.Read {
			writeError(w, "invalid token", http.StatusForbidden)
			return
		}
		if len(tokenParts) == 2 && tokenParts[1] == cfg.Tokens.ReadWrite {
			readOnly = false
		}

		var activeDB *DB
		if readOnly {
			dbRO, err := OpenDB("kleikat.db", true)
			if err != nil {
				writeError(w, `{"error":"db error"}`, http.StatusInternalServerError)
				return
			}
			defer dbRO.Close()
			activeDB = dbRO
		} else {
			activeDB = db
		}

		// /api/schema/{name}
		if len(parts) >= 2 && parts[0] == "schema" {
			schema := parts[1]

			// GET /api/schema/{name} - list entries
			if r.Method == http.MethodGet && len(parts) == 2 {
				search := r.URL.Query().Get("search")
				filters := make(map[string][]string)
				for k, v := range r.URL.Query() {
					if strings.HasPrefix(k, "f.") && len(v) > 0 {
						filters[k[2:]] = strings.Split(v[0], FILTER_SEP)
					}
				}
				entries, err := activeDB.GetEntries(schema, search, filters)
				if err != nil {
					writeError(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeJSON(w, entries)
				return
			}

			// GET /api/schema/{name}/distincts
			if r.Method == http.MethodGet && len(parts) == 3 && parts[2] == "distincts" {
				vals, err := activeDB.GetDistinctValues(schema)
				if err != nil {
					writeError(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeJSON(w, vals)
				return
			}

			// GET /api/schema/{name}/autocomplete?attribute=...&fragment=...
			if r.Method == http.MethodGet && len(parts) == 3 && parts[2] == "autocomplete" {
				attr := r.URL.Query().Get("attribute")
				fragment := r.URL.Query().Get("fragment")
				vals, err := activeDB.GetAutocomplete(schema, attr, fragment)
				if err != nil {
					writeError(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeJSON(w, vals)
				return
			}

			// POST /api/schema/{name} - add entry
			if r.Method == http.MethodPost && len(parts) == 2 {
				if err := activeDB.CheckWrite(); err != nil {
					writeError(w, err.Error(), http.StatusForbidden)
					return
				}
				var body struct {
					EntryID string            `json:"entry_id"`
					Attrs   map[string]string `json:"attrs"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					writeError(w, "invalid json", http.StatusBadRequest)
					return
				}
				if body.EntryID == "" {
					body.EntryID = generateID()
				}
				if err := activeDB.AddEntry(schema, body.EntryID, body.Attrs); err != nil {
					writeError(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeJSON(w, map[string]string{"entry_id": body.EntryID})
				return
			}

			// PUT /api/schema/{name}/{entry_id} - update entry
			if r.Method == http.MethodPut && len(parts) == 3 {
				if err := activeDB.CheckWrite(); err != nil {
					writeError(w, err.Error(), http.StatusForbidden)
					return
				}
				var body struct {
					Attrs map[string]string `json:"attrs"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					writeError(w, "invalid json", http.StatusBadRequest)
					return
				}
				if err := activeDB.UpdateEntry(schema, parts[2], body.Attrs); err != nil {
					writeError(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeJSON(w, map[string]string{"ok": "true"})
				return
			}

			// DELETE /api/schema/{name}/{entry_id} - delete entry
			if r.Method == http.MethodDelete && len(parts) == 3 {
				if err := activeDB.CheckWrite(); err != nil {
					writeError(w, err.Error(), http.StatusForbidden)
					return
				}
				if err := activeDB.DeleteEntry(schema, parts[2]); err != nil {
					writeError(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeJSON(w, map[string]string{"ok": "true"})
				return
			}
		}

		writeError(w, "not found", http.StatusNotFound)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":"marshal failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.WriteHeader(code)
	writeJSON(w, map[string]string{"error": msg})
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
