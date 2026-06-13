package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const FILTER_SEP = "^"
const IMAGE_MAX_SIZE = 10 << 20 // 10MB max
const DB_FILENAME = "kleikat.db"

var (
	imageBaseDir         string
	imageTempDir         string
	imageThumbDir        string
	isValidImageFilename = regexp.MustCompile(`\A[0-9a-f]{8,32}\.[a-z]{3,4}\z`).MatchString
)

func classifyImage(cfg *Config, db *DB, imagePath string, schemaName string) {
	if cfg.LLM.URL == "" || cfg.LLM.APIKey == "" {
		return
	}

	schema := cfg.Schemas[schemaName]
	if schema.Title == "" {
		return
	}

	// Build attribute list for prompt
	attrsWithExamples := make(map[string][]string)
	for _, attr := range schema.AttributeTitles {
		var err error
		attrsWithExamples[attr[0]], err = db.GetDistinctValuesTopK(schemaName, attr[0], 200)
		if err != nil {
			log.Printf("GetDistinctValuesTopK failed: %v", err)
			return
		}
	}

	// Read image file
	data, err := os.ReadFile(imagePath)
	if err != nil {
		log.Printf("Failed to read image for classification: %v", err)
		return
	}

	// Build prompt with attribute context
	j, err := json.Marshal(attrsWithExamples)
	if err != nil {
		log.Printf("Failed to marshal attributes/examples: %v", err)
		return
	}
	prompt := fmt.Sprintf(`Du bist ein Klassifizierungshelfer. Analysiere das Bild und ordne die Attribute zu.

Schema: %s
Unterstützte Attribute: %s

Verwende nur diese Attribute und gib JSON zurück im Format:
{"attribute1": "wert1", "attribute2": "wert2"}

Werte, die du nicht aus dem Bild ablesen kannst, weglassen.
Falls nötig, neue Werte hinzufügen.
Größenangaben nur, wenn du das Etikett lesen kannst.`,
		schema.Title,
		j,
	)

	// Encode image as base64 data URL
	encoded := base64.StdEncoding.EncodeToString(data)
	dataURL := "data:image/jpeg;base64," + encoded

	// Build JSON request body for OpenAI-compatible chat completions API
	requestBody := map[string]interface{}{
		"model": "llama-3.2-vision",
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": prompt},
					{"type": "image_url", "image_url": map[string]interface{}{"url": dataURL}},
				},
			},
		},
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		log.Printf("Failed to marshal request body: %v", err)
		return
	}

	// Send request to LLM
	req, err := http.NewRequest("POST", cfg.LLM.URL, bytes.NewReader(jsonData))
	if err != nil {
		log.Printf("Failed to create request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.LLM.APIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("LLM request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		log.Printf("LLM API error %d: %s", resp.StatusCode, string(errBody))
		return
	}

	// Parse OpenAI chat completions response
	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &chatResp); err != nil || len(chatResp.Choices) == 0 {
		// Fallback: try parsing as direct JSON
		var fallback map[string]string
		if err := json.Unmarshal(body, &fallback); err == nil {
			result = fallback
		} else {
			// Try to extract JSON from text response
			jsonStr := extractJSON(string(body))
			if jsonStr != "" {
				json.Unmarshal([]byte(jsonStr), &result)
			}
		}
	} else {
		json.Unmarshal([]byte(chatResp.Choices[0].Message.Content), &result)
	}

	if result == nil || len(result) == 0 {
		log.Printf("LLM returned empty result")
		return
	}

	// Store results in database
	filename := filepath.Base(imagePath)
	err = db.StoreSuggestions(filename, result)
	if err != nil {
		log.Printf("Failed to store suggestions: %v", err)
	}
}

func extractJSON(s string) string {
	// Try to find JSON object in the string
	start := strings.Index(s, "{")
	if start == -1 {
		return ""
	}
	// Find matching closing brace
	depth := 0
	for i := start; i < len(s); i++ {
		if s[i] == '{' {
			depth++
		} else if s[i] == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

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

	imagePath := cfg.ImagePath
	if imagePath != "" {
		imageBaseDir = imagePath
		imageTempDir = filepath.Join(imagePath, "temp")
		imageThumbDir = filepath.Join(imagePath, "thumbs")
		if err := os.MkdirAll(imagePath, 0755); err != nil {
			log.Fatalf("Create image directory: %v", err)
		}
		if err := os.MkdirAll(imageTempDir, 0755); err != nil {
			log.Fatalf("Create temp image directory: %v", err)
		}
		if err := os.MkdirAll(imageThumbDir, 0755); err != nil {
			log.Fatalf("Create thumbnail directory: %v", err)
		}
	}

	db, err := OpenDB(DB_FILENAME, false)
	if err != nil {
		log.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	// Serve static files
	fs := http.FileServer(http.Dir("assets"))
	http.Handle("/", fs)

	// Serve images if configured
	if imagePath != "" {
		http.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir(imagePath))))
	}

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
			dbRO, err := OpenDB(DB_FILENAME, true)
			if err != nil {
				writeError(w, "db error", http.StatusInternalServerError)
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
				entries, err := activeDB.GetEntries(schema, search, filters, cfg.Schemas[schema].Sort)
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

				// Process image if present
				if imgPath, ok := body.Attrs["image"]; ok {
					body.Attrs["image"] = processTempImage(imgPath, body.EntryID)
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
				entryID := parts[2]

				// Delete associated image
				go deleteEntryImages(entryID)

				if err := activeDB.DeleteEntry(schema, entryID); err != nil {
					writeError(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeJSON(w, map[string]string{"ok": "true"})
				return
			}

			// POST /api/schema/{name}/{entry_id}/upload-image - upload image
			if r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "upload-image" {
				if err := activeDB.CheckWrite(); err != nil {
					writeError(w, err.Error(), http.StatusForbidden)
					return
				}

				if imageBaseDir == "" {
					writeError(w, "image upload not configured", http.StatusForbidden)
					return
				}

				filename, err := saveUploadToTemp(r)
				if err != nil {
					writeError(w, err.Error(), http.StatusBadRequest)
					return
				}

				// Trigger LLM classification in background if configured
				go classifyImage(cfg, activeDB, filepath.Join(imageTempDir, filename), schema)

				writeJSON(w, map[string]string{"filename": filename})
				return
			}

			// GET /api/schema/{name}/classify - trigger classification for an image
			if r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "classify" {
				if imageBaseDir == "" {
					writeError(w, "image classification not configured", http.StatusForbidden)
					return
				}

				var body struct {
					Filename string `json:"filename"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					writeError(w, "invalid json", http.StatusBadRequest)
					return
				}
				if body.Filename == "" {
					writeError(w, "filename required", http.StatusBadRequest)
					return
				}

				// Check if already classified
				suggestions, err := activeDB.GetSuggestions(body.Filename)
				if err == nil && len(suggestions) > 0 {
					writeJSON(w, suggestions)
					return
				}

				// Try to find image in base dir or temp dir
				imagePath := filepath.Join(imageBaseDir, body.Filename)
				if _, err := os.Stat(imagePath); os.IsNotExist(err) {
					imagePath = filepath.Join(imageTempDir, body.Filename)
				}
				if _, err := os.Stat(imagePath); os.IsNotExist(err) {
					writeError(w, "image not found", http.StatusNotFound)
					return
				}

				// Trigger classification in background
				go classifyImage(cfg, activeDB, imagePath, schema)

				writeJSON(w, map[string]string{"status": "started"})
				return
			}

			// GET /api/schema/{name}/suggestions/{filename} - get stored suggestions
			if r.Method == http.MethodGet && len(parts) >= 4 && parts[2] == "suggestions" {
				filename := parts[3]
				suggestions, err := activeDB.GetSuggestions(filename)
				if err != nil {
					writeJSON(w, map[string]interface{}{})
					return
				}
				writeJSON(w, suggestions)
				return
			}
		}

		writeError(w, "not found", http.StatusNotFound)
	}
}

func saveUploadToTemp(r *http.Request) (string, error) {
	// Read multipart form
	err := r.ParseMultipartForm(IMAGE_MAX_SIZE)
	if err != nil {
		return "", fmt.Errorf("parse form: %v", err)
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		return "", fmt.Errorf("no image file provided")
	}
	defer file.Close()

	// Validate content type
	contentType := header.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		// Also check by extension
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".gif" && ext != ".webp" {
			return "", fmt.Errorf("unsupported image type: %s", header.Filename)
		}
	}

	// Generate unique filename
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".jpg"
	}
	tempFilename := generateID() + ext
	tempPath := filepath.Join(imageTempDir, tempFilename)

	out, err := os.Create(tempPath)
	if err != nil {
		return "", fmt.Errorf("create file: %v", err)
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	if err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("save file: %v", err)
	}

	return tempFilename, nil
}

func processTempImage(tempFilename, entryID string) string {
	if imageBaseDir == "" || !isValidImageFilename(tempFilename) {
		return ""
	}

	tempPath := filepath.Join(imageTempDir, tempFilename)
	ext := strings.ToLower(filepath.Ext(tempFilename))
	finalFilename := entryID + ext
	finalPath := filepath.Join(imageBaseDir, finalFilename)

	// Move temp file to final location
	err := os.Rename(tempPath, finalPath)
	if err != nil {
		return ""
	}

	// Generate thumbnail in background
	go generateThumbnail(finalPath, finalFilename)

	return finalFilename
}

func generateThumbnail(imagePath, filename string) {
	thumbPath := filepath.Join(imageThumbDir, filename)

	// Check if imagemagick is available
	_, err := exec.LookPath("magick")
	if err != nil {
		log.Printf("ImageMagick not found, skipping thumbnail generation")
		return
	}

	// Generate 32x32 thumbnail
	cmd := exec.Command("convert", imagePath, "-thumbnail", "32x32", thumbPath)
	if err := cmd.Run(); err != nil {
		log.Printf("Failed to generate thumbnail: %v", err)
		return
	}
}

func deleteEntryImages(entryID string) {
	if imageBaseDir == "" {
		return
	}

	exts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
	for _, ext := range exts {
		imgPath := filepath.Join(imageBaseDir, entryID+ext)
		os.Remove(imgPath)

		thumbPath := filepath.Join(imageThumbDir, entryID+ext)
		os.Remove(thumbPath)
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
