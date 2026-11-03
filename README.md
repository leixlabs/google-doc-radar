# Google Doc Radar

Chrome extension + Go server for capturing and querying Google Docs content.

## Architecture

```
Chrome Extension (docs.google.com)
  │ content.js: extract document content from DOM
  │ background.js: relay to server via HTTP
  ▼
Go Server (localhost:9906)
  │ POST /api/docs — ingest (SQLite + Markdown mirror)
  │ GET /api/read — query (JSON/MD/HTML)
  │ GET /api/search — full-text search
  ▼
SQLite (gdoc.db)  +  Markdown files (--md-dir)
```

## Features

- **Multi-tab support**: Each document tab is saved separately
- **Auto-extract**: Captures content on navigation and on edits (debounced)
- **Tab-aware**: Detects tab switches and re-extracts
- **Search**: Full-text search across document titles and content
- **Multiple output formats**: JSON, Markdown, HTML (with outline sidebar)
- **Markdown mirror**: Optionally writes every saved doc to a `.md` file on disk

## Quick Start

### Server

```bash
cd server
CGO_ENABLED=1 go build -o gdoc-radar-server .
./gdoc-radar-server --data-dir ./data --port 9906 --md-dir ./data/md
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

### Server flags

| Flag | Default | Description |
|------|---------|-------------|
| `--data-dir` | `.` | Directory holding `gdoc.db` |
| `--port` | `9906` | Listen port |
| `--host` | `0.0.0.0` | Listen host |
| `--md-dir` | `$GDOC_MD_DIR` | Mirror every ingested doc to a `.md` file here (empty = disabled) |
| `--md-backfill` | `false` | On startup, export all existing DB rows into `--md-dir` |

### POST /api/docs — Ingest

```bash
curl -X POST http://localhost:9906/api/docs \
  -H 'Content-Type: application/json' \
  -d '{"document_id":"abc123","title":"My Doc","content":"Hello world"}'
```

Response:

```json
{"upserted": 1, "markdown": "/data/md/My Doc [abc123].md"}
```

`markdown` is `null` when `--md-dir` is not set. The DB write always wins: if the
file cannot be written the request still succeeds and the failure comes back as
`markdown_error`.

### Markdown mirror

With `--md-dir DIR`, each ingest writes one Markdown file next to the DB row:

- **One file per `(document_id, tab_id)`** — the same key the DB uses, so a
  re-ingest overwrites the file in place.
- **File name**: `<title> [<document_id> - <tab_id>].md` (tab part omitted for
  single-tab docs). Title is sanitized for the filesystem; the ID keeps names
  unique and stable.
- **Renames**: when a doc/tab title changes, the old file is deleted — no
  `Title (1).md` leftovers.
- **Writes are atomic** (temp file + rename), so a watcher — Obsidian, Logseq —
  never reads a half-written file.
- **Content** is the Google Docs Markdown export, prefixed with YAML front
  matter:

```markdown
---
document_id: "abc123"
tab_id: "t.0"
tab_name: "Meeting Notes"
title: "Q4 Planning"
url: "https://docs.google.com/document/d/abc123/edit"
created_at: "2026-11-03T11:04:33Z"
updated_at: "2026-11-03T11:04:33Z"
source: "google-doc-radar"
---

# Q4 Planning
...
```

Existing rows are not written retroactively unless you pass `--md-backfill`
once at startup.

### GET /api/read — Query

```bash
# By document ID (returns all tabs)
curl "http://localhost:9906/api/read?document_id=abc123"

# By tab
curl "http://localhost:9906/api/read?document_id=abc123&tab_id=t.0"

# Time range
curl "http://localhost:9906/api/read?from=2026-01-01&to=2026-06-30"
```

### GET /api/search — Search

```bash
curl "http://localhost:9906/api/search?q=meeting+notes"
```

### GET /api/read/md — Markdown

```bash
curl "http://localhost:9906/api/read/md?document_id=abc123"
```

### GET /api/read/html — HTML with outline

Open in browser: `http://localhost:9906/api/read/html?document_id=abc123`

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
