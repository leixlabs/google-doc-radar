# Google Doc Radar

Chrome extension + Go server for capturing and querying Google Docs content.

## Architecture

```
Chrome Extension (docs.google.com)
  │ content.js: extract document content from DOM
  │ background.js: relay to server via HTTP
  ▼
Go Server (localhost:9902)
  │ POST /api/docs — ingest
  │ GET /api/read — query (JSON/MD/HTML)
  │ GET /api/search — full-text search
  ▼
SQLite (gdoc.db)
```

## Features

- **Multi-tab support**: Each document tab is saved separately
- **Auto-extract**: Captures content on navigation and on edits (debounced)
- **Tab-aware**: Detects tab switches and re-extracts
- **Search**: Full-text search across document titles and content
- **Multiple output formats**: JSON, Markdown, HTML (with outline sidebar)

## Quick Start

### Server

```bash
cd server
CGO_ENABLED=1 go build -o gdoc-radar-server .
./gdoc-radar-server --data-dir ./data --port 9902
```

Or with Docker:

```bash
docker-compose up -d
```

### Extension

1. Open `chrome://extensions/`
2. Enable "Developer mode"
3. Click "Load unpacked" → select `radar-extension/`
4. Open any Google Doc — content is auto-captured

### Install as macOS service

```bash
cd server && bash install.sh
```

## API

### POST /api/docs — Ingest

```bash
curl -X POST http://localhost:9902/api/docs \
  -H 'Content-Type: application/json' \
  -d '{"document_id":"abc123","title":"My Doc","content":"Hello world"}'
```

### GET /api/read — Query

```bash
# By document ID (returns all tabs)
curl "http://localhost:9902/api/read?document_id=abc123"

# By tab
curl "http://localhost:9902/api/read?document_id=abc123&tab_id=t.0"

# Time range
curl "http://localhost:9902/api/read?from=2026-01-01&to=2026-06-30"
```

### GET /api/search — Search

```bash
curl "http://localhost:9902/api/search?q=meeting+notes"
```

### GET /api/read/md — Markdown

```bash
curl "http://localhost:9902/api/read/md?document_id=abc123"
```

### GET /api/read/html — HTML with outline

Open in browser: `http://localhost:9902/api/read/html?document_id=abc123`

## Data Model

Each row in SQLite represents one tab of a document:

| Column | Type | Description |
|--------|------|-------------|
| document_id | TEXT | Google Doc ID |
| tab_id | TEXT | Tab identifier (empty for single-tab docs) |
| tab_name | TEXT | Display name of the tab |
| title | TEXT | Document title |
| content | TEXT | Extracted text content |
| url | TEXT | Document URL |
| created_at | DATETIME | First ingestion time |
| updated_at | DATETIME | Last update time |

Unique constraint: `(document_id, tab_id)`
