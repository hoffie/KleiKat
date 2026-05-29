# KleiKat

A lightweight web application for managing home inventory items — clothing, shoes, books, and more.

Categorize items by multiple properties and search/filter them quickly. Designed to work smoothly on phones and tablets.

## Features

- **Multiple schemas** — Define different item types (clothing, shoes, books) with custom attributes
- **Smart search** — Full-text search across all columns
- **Excel-style filters** — Multi-select column filters with autocomplete suggestions
- **Quick entry** — Type-ahead autocomplete for fast data entry
- **Edit & delete** — Inline editing and deletion of entries
- **Shareable views** — Copy filtered views as shareable links or QR codes
- **CSV import** — Bulk import items via CSV files
- **Read-only mode** — Share read-only access without write permissions
- **Responsive UI** — Works on desktop, tablet, and mobile devices

## Tech Stack

- **Backend:** Go (1.25+)
- **Database:** SQLite (modernc.org/sqlite — pure Go, no CGO required)
- **Frontend:** Vanilla HTML, CSS, JavaScript (single-page application)
- **Schema config:** YAML

## Getting Started

### Prerequisites

- Go 1.25 or later
- A SQLite database file (created automatically on first run)

### Configuration

1. Copy the example configuration files:

```bash
cp config.yaml.example config.yaml
cp schemas.yaml.example schemas.yaml
```

2. Edit `config.yaml` to set your access tokens:

```yaml
tokens:
  read: "your-read-token"
  read_write: "your-read-write-token"
```

3. Edit `schemas.yaml` to define your item schemas and attributes:

```yaml
clothing:
  title: "Kleidung"
  attribute_titles:
    - [type, Art, Arten]
    - [sub_type, Sub-Art, Sub-Arten]
    - [material, Material, Materialien]
    - [size, Größe, Größen]
    - [color, Farbe, Farben]
    - [property, Merkmal, Merkmale]
    - [location, Ort, Orte]
```

### Running the Server

```bash
go build -o kleikat
./kleikat -port 9000
```

Visit `http://localhost:9000/?t=your-read-token.your-read-write-token` in your browser.

## Usage

### Adding Items

1. Enter values in the first row of input fields
2. Autocomplete suggestions appear as you type
3. Press Enter or click the add button to store the entry

### Searching & Filtering

- Use the **search field** to find entries matching text across all columns
- Click **column headers** to open multi-select filters
- Selected filters are applied automatically or via the "Anwenden" button
- Active filter tags appear in column headers; click [X] to remove them

### Sharing Views

- **Link teilen** (🎯) — Copies the current URL with all filters to clipboard
- **QR-Code drucken** (🖨) — Opens a printable QR code linking to the current filtered view

### Editing & Deleting

- Click the edit button (✏️) on any row to transform it into editable fields
- Click the delete button (🗑) to remove an entry (confirmation required)

### CSV Import

Import items from a CSV file:

```bash
./kleikat import -schema clothing items.csv
```

The CSV file should have headers matching attribute names or their display titles (e.g., `Größe`, `Farbe`, `Material`).

## API

All API endpoints (except `/api/schemas`) require authentication via the `X-Token` header or `token` query parameter.

### Authentication

| Token Type | Header | Access Level |
|------------|--------|--------------|
| Read | `X-Token: <read-token>` | Read-only |
| Read/Write | `X-Token: <read-token>.<read-write-token>` | Full access |

### Endpoints

#### Get Schema Information

```
GET /api/schemas
```

Returns all configured schemas with their attribute titles.

#### List Entries

```
GET /api/schema/{name}?search=...&f.{attribute}=...
```

- `search` — Full-text search across all attributes
- `f.{attribute}` — Filter by attribute (can be specified multiple times)

Example: `GET /api/schema/clothing?search=blau&f.color=Blau&f.color=Rot`

#### Get Distinct Values

```
GET /api/schema/{name}/distincts
```

Returns all unique values for each attribute (used for filter dropdowns).

#### Autocomplete

```
GET /api/schema/{name}/autocomplete?attribute={attr}&fragment={text}
```

Returns matching values for the given attribute and text fragment.

#### Add Entry

```
POST /api/schema/{name}
Content-Type: application/json

{
  "entry_id": "optional-custom-id",
  "attrs": {
    "color": "Blau",
    "size": "M",
    "material": "Baumwolle"
  }
}
```

#### Update Entry

```
PUT /api/schema/{name}/{entry_id}
Content-Type: application/json

{
  "attrs": {
    "color": "Rot",
    "size": "L"
  }
}
```

#### Delete Entry

```
DELETE /api/schema/{name}/{entry_id}
```

## Database Schema

The SQLite database uses a normalized EAV (Entity-Attribute-Value) structure:

| Column | Description |
|--------|-------------|
| `schema` | Schema name (e.g., "clothing", "shoes") |
| `entry_id` | Unique identifier for each entry |
| `attribute` | Attribute key (e.g., "color", "size") |
| `value` | Attribute value |

**Constraints:** `(entry_id, attribute)` is UNIQUE.

## Security

- **config.yaml is never served** as a static file
- Tokens are stored in `localStorage` and sent as headers, not URL parameters
- The URL contains a sanitized token for sharing; the original token is removed after loading
- Write operations require the read-write token
- Read-only mode opens the database in read-only mode

## Development

### Running Tests

```bash
go test ./...
```

### Building

```bash
go build -o kleikat
```

### Project Structure

```
├── main.go          # Server entry point and HTTP handlers
├── db.go            # Database operations (SQLite)
├── model.go         # Data model definitions
├── config.go        # Configuration loading
├── index.html       # Frontend SPA
├── static/
│   └── style.css    # Styles
├── schemas.yaml     # Schema definitions
├── config.yaml      # Access tokens
├── kleikat.db       # SQLite database (created at runtime)
└── kleikat_test.go  # Tests
```

## License

This project is for personal/home use.
