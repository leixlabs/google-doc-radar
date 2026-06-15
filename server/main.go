package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ── Types ────────────────────────────────────────────────────────────────────

type TabInfo struct {
	TabID   string `json:"tab_id"`
	TabName string `json:"tab_name"`
}

type DocRequest struct {
	DocumentID string  `json:"document_id"`
	TabID      *string `json:"tab_id"`
	TabName    *string `json:"tab_name"`
	Title      *string `json:"title"`
	Content    *string `json:"content"`
	URL        *string `json:"url"`
}

type ReadDoc struct {
	ID         int64   `json:"id"`
	DocumentID string  `json:"document_id"`
	TabID      *string `json:"tab_id"`
	TabName    *string `json:"tab_name"`
	Title      *string `json:"title"`
	Content    *string `json:"content"`
	URL        *string `json:"url"`
	CreatedAt  *string `json:"created_at"`
	UpdatedAt  *string `json:"updated_at"`
}

type ReadResponse struct {
	Count     int       `json:"count"`
	Filters   ReadQuery `json:"filters"`
	Documents []ReadDoc `json:"documents"`
}

type ReadQuery struct {
	DocumentID string
	TabID      string
	TabName    string
	Query      string
	From       string
	To         string
	Limit      int
}

type SearchResult struct {
	Count     int       `json:"count"`
	Query     string    `json:"query"`
	Documents []ReadDoc `json:"documents"`
}

// ── Database ─────────────────────────────────────────────────────────────────

const schemaSQL = `
CREATE TABLE IF NOT EXISTS documents (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id TEXT NOT NULL,
    tab_id      TEXT NOT NULL DEFAULT '',
    tab_name    TEXT,
    title       TEXT,
    content     TEXT,
    url         TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(document_id, tab_id)
);
CREATE INDEX IF NOT EXISTS idx_doc_id      ON documents(document_id);
CREATE INDEX IF NOT EXISTS idx_doc_title   ON documents(title);
CREATE INDEX IF NOT EXISTS idx_doc_updated ON documents(updated_at);
`

func initDB(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dbPath := filepath.Join(dataDir, "gdoc.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return db, nil
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func handlePostDocs(db *sql.DB) http.HandlerFunc {
	const upsertSQL = `
INSERT INTO documents (document_id, tab_id, tab_name, title, content, url, updated_at)
VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(document_id, tab_id) DO UPDATE SET
    tab_name   = excluded.tab_name,
    title      = excluded.title,
    content    = excluded.content,
    url        = excluded.url,
    updated_at = CURRENT_TIMESTAMP
`
	return func(w http.ResponseWriter, r *http.Request) {
		var req DocRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON: `+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		if req.DocumentID == "" {
			http.Error(w, `{"error":"document_id required"}`, http.StatusBadRequest)
			return
		}

		tabID := ""
		if req.TabID != nil {
			tabID = *req.TabID
		}

		res, err := db.Exec(upsertSQL, req.DocumentID, tabID, req.TabName, req.Title, req.Content, req.URL)
		if err != nil {
			http.Error(w, `{"error":"`+jsonEscape(err.Error())+`"}`, http.StatusInternalServerError)
			return
		}
		n, _ := res.RowsAffected()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"upserted":%d}`, n)
	}
}

func parseTimeFlexible(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty")
	}
	// Try common formats
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	// Try Unix timestamp (seconds or milliseconds)
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		if ts > 1e12 {
			return time.UnixMilli(ts), nil
		}
		return time.Unix(ts, 0), nil
	}
	return time.Time{}, fmt.Errorf("cannot parse time: %s", s)
}

func parseReadQuery(r *http.Request) ReadQuery {
	q := r.URL.Query()
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := q.Get(k); v != "" {
				return v
			}
		}
		return ""
	}
	limit := 1000
	if s := q.Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
		}
	}
	return ReadQuery{
		DocumentID: get("document_id", "doc_id"),
		TabID:      get("tab_id"),
		TabName:    get("tab_name"),
		Query:      get("q", "query", "search"),
		From:       get("from", "since", "start"),
		To:         get("to", "until", "end"),
		Limit:      limit,
	}
}

func buildReadQuery(rq ReadQuery) (string, []interface{}) {
	var conds []string
	var args []interface{}

	if rq.DocumentID != "" {
		conds = append(conds, "document_id = ?")
		args = append(args, rq.DocumentID)
	}
	if rq.TabID != "" {
		conds = append(conds, "tab_id = ?")
		args = append(args, rq.TabID)
	}
	if rq.TabName != "" {
		conds = append(conds, "tab_name LIKE ?")
		args = append(args, "%"+rq.TabName+"%")
	}
	if rq.From != "" {
		if t, err := parseTimeFlexible(rq.From); err == nil {
			conds = append(conds, "updated_at >= ?")
			args = append(args, t.Format("2006-01-02 15:04:05"))
		}
	}
	if rq.To != "" {
		if t, err := parseTimeFlexible(rq.To); err == nil {
			conds = append(conds, "updated_at <= ?")
			args = append(args, t.Format("2006-01-02 15:04:05"))
		}
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	query := fmt.Sprintf("SELECT id, document_id, tab_id, tab_name, title, content, url, created_at, updated_at FROM documents %s ORDER BY updated_at DESC LIMIT ?", where)
	args = append(args, rq.Limit)
	return query, args
}

func queryDocs(db *sql.DB, rq ReadQuery) ([]ReadDoc, error) {
	query, args := buildReadQuery(rq)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []ReadDoc
	for rows.Next() {
		var d ReadDoc
		if err := rows.Scan(&d.ID, &d.DocumentID, &d.TabID, &d.TabName, &d.Title, &d.Content, &d.URL, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, nil
}

func handleReadDocs(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rq := parseReadQuery(r)
		docs, err := queryDocs(db, rq)
		if err != nil {
			http.Error(w, `{"error":"`+jsonEscape(err.Error())+`"}`, http.StatusInternalServerError)
			return
		}
		if docs == nil {
			docs = []ReadDoc{}
		}
		resp := ReadResponse{Count: len(docs), Filters: rq, Documents: docs}
		writeJSON(w, resp)
	}
}

func handleSearchDocs(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			q = r.URL.Query().Get("query")
		}
		if q == "" {
			http.Error(w, `{"error":"q or query parameter required"}`, http.StatusBadRequest)
			return
		}

		limit := 20
		if s := r.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				limit = n
			}
		}

		like := "%" + q + "%"
		rows, err := db.Query(
			"SELECT id, document_id, tab_id, tab_name, title, content, url, created_at, updated_at FROM documents WHERE title LIKE ? OR content LIKE ? ORDER BY updated_at DESC LIMIT ?",
			like, like, limit,
		)
		if err != nil {
			http.Error(w, `{"error":"`+jsonEscape(err.Error())+`"}`, http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var docs []ReadDoc
		for rows.Next() {
			var d ReadDoc
			if err := rows.Scan(&d.ID, &d.DocumentID, &d.TabID, &d.TabName, &d.Title, &d.Content, &d.URL, &d.CreatedAt, &d.UpdatedAt); err != nil {
				http.Error(w, `{"error":"`+jsonEscape(err.Error())+`"}`, http.StatusInternalServerError)
				return
			}
			docs = append(docs, d)
		}
		if docs == nil {
			docs = []ReadDoc{}
		}
		writeJSON(w, SearchResult{Count: len(docs), Query: q, Documents: docs})
	}
}

func applyDefaultDateRange(rq *ReadQuery) {
	// Only apply the 7-day default when no specific doc/tab filter is set
	if rq.From == "" && rq.To == "" && rq.DocumentID == "" && rq.TabID == "" {
		rq.From = time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	}
}

func handleReadMD(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rq := parseReadQuery(r)
		applyDefaultDateRange(&rq)
		docs, err := queryDocs(db, rq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		fmt.Fprint(w, renderMarkdown(docs))
	}
}

func handleReadHTML(db *sql.DB, tpl string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rq := parseReadQuery(r)
		if rq.From == "" && rq.To == "" && rq.DocumentID == "" && rq.TabID == "" {
			rq.From = time.Now().AddDate(0, 0, -7).Format("2006-01-02")
		}
		docs, err := queryDocs(db, rq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		md := renderMarkdown(docs)
		htmlContent := markdownToHTML(md)
		pageTitle := "Google Docs Radar"
		if rq.DocumentID != "" {
			pageTitle = "Doc: " + rq.DocumentID
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page := strings.Replace(tpl, "{{.PageTitle}}", htmlEscape(pageTitle), 1)
		page = strings.Replace(page, "{{.Content}}", htmlContent, 1)
		fmt.Fprint(w, page)
	}
}

// ── Markdown rendering ───────────────────────────────────────────────────────

func renderMarkdown(docs []ReadDoc) string {
	if len(docs) == 0 {
		return "No documents found.\n"
	}

	// Group by date
	type docGroup struct {
		date string
		docs []ReadDoc
	}
	groupMap := map[string][]ReadDoc{}
	for _, d := range docs {
		date := "Unknown date"
		if d.UpdatedAt != nil && *d.UpdatedAt != "" {
			if t, err := parseTimeFlexible(*d.UpdatedAt); err == nil {
				date = t.Format("2006-01-02")
			}
		}
		groupMap[date] = append(groupMap[date], d)
	}

	var groups []docGroup
	for date, docs := range groupMap {
		groups = append(groups, docGroup{date: date, docs: docs})
	}
	// Sort newest first
	for i := 0; i < len(groups); i++ {
		for j := i + 1; j < len(groups); j++ {
			if groups[j].date > groups[i].date {
				groups[i], groups[j] = groups[j], groups[i]
			}
		}
	}

	var b strings.Builder
	for _, g := range groups {
		fmt.Fprintf(&b, "## %s\n\n", g.date)
		for _, d := range g.docs {
			title := "Untitled"
			if d.Title != nil && *d.Title != "" {
				title = *d.Title
			}
			tabInfo := ""
			if d.TabName != nil && *d.TabName != "" {
				tabInfo = fmt.Sprintf(" (tab: %s)", *d.TabName)
			}
			fmt.Fprintf(&b, "### %s%s\n\n", title, tabInfo)

			if d.Content != nil && *d.Content != "" {
				fmt.Fprintf(&b, "%s\n\n", *d.Content)
			}
			if d.URL != nil && *d.URL != "" {
				fmt.Fprintf(&b, "[Open in Google Docs](%s)\n\n", *d.URL)
			}
		}
	}
	return b.String()
}

// ── Markdown to HTML ─────────────────────────────────────────────────────────

var (
	reH2   = regexp.MustCompile(`(?m)^## (.+)$`)
	reH3   = regexp.MustCompile(`(?m)^### (.+)$`)
	reLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

func markdownToHTML(md string) string {
	s := htmlEscape(md)
	// Headings (order matters: h3 before h2)
	s = reH3.ReplaceAllString(s, `<h3 id="$1">$1</h3>`)
	s = reH2.ReplaceAllString(s, `<h2 id="$1">$1</h2>`)
	// Links
	s = reLink.ReplaceAllString(s, `<a href="$2" target="_blank">$1</a>`)
	// Paragraphs
	s = strings.ReplaceAll(s, "\n\n", "</p>\n<p>")
	s = "<p>" + s + "</p>"
	return s
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// ── CORS middleware ──────────────────────────────────────────────────────────

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── API docs page ────────────────────────────────────────────────────────────

func handleAPIDocs(tpl string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, tpl)
	}
}

// ── Main ─────────────────────────────────────────────────────────────────────

func main() {
	dataDir := flag.String("data-dir", ".", "directory for gdoc.db")
	port := flag.Int("port", 9906, "server port")
	host := flag.String("host", "0.0.0.0", "server host")
	flag.Parse()

	db, err := initDB(*dataDir)
	if err != nil {
		log.Fatalf("init db: %v", err)
	}
	defer db.Close()

	// Load templates
	pageHTML, _ := os.ReadFile(filepath.Join("templates", "page.html"))
	apiDocsHTML, _ := os.ReadFile(filepath.Join("templates", "api-docs.html"))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleAPIDocs(string(apiDocsHTML)))
	mux.HandleFunc("POST /api/docs", handlePostDocs(db))
	mux.HandleFunc("GET /api/read", handleReadDocs(db))
	mux.HandleFunc("GET /api/read/md", handleReadMD(db))
	mux.HandleFunc("GET /api/read/html", handleReadHTML(db, string(pageHTML)))
	mux.HandleFunc("GET /api/search", handleSearchDocs(db))

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("google-doc-radar server listening on %s (data: %s)", addr, *dataDir)
	if err := http.ListenAndServe(addr, cors(mux)); err != nil {
		log.Fatalf("server: %v", err)
	}
}
