package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := initDB(dir)
	if err != nil {
		t.Fatalf("initDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestPostAndGetDocs(t *testing.T) {
	db := setupTestDB(t)
	handler := handlePostDocs(db)

	// Ingest a document
	body := `{"document_id":"doc1","tab_id":"t.0","tab_name":"Notes","title":"My Doc","content":"Hello world","url":"https://docs.google.com/document/d/doc1/edit"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != 200 {
		t.Fatalf("POST /api/docs: status=%d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["upserted"].(float64) != 1 {
		t.Fatalf("expected upserted=1, got %v", resp["upserted"])
	}

	// Read it back
	readHandler := handleReadDocs(db)
	req = httptest.NewRequest("GET", "/api/read?document_id=doc1", nil)
	w = httptest.NewRecorder()
	readHandler(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /api/read: status=%d", w.Code)
	}

	var readResp ReadResponse
	json.Unmarshal(w.Body.Bytes(), &readResp)
	if readResp.Count != 1 {
		t.Fatalf("expected 1 doc, got %d", readResp.Count)
	}
	if readResp.Documents[0].DocumentID != "doc1" {
		t.Fatalf("expected doc1, got %s", readResp.Documents[0].DocumentID)
	}
	if *readResp.Documents[0].TabName != "Notes" {
		t.Fatalf("expected tab_name=Notes, got %s", *readResp.Documents[0].TabName)
	}
}

func TestUpsertConflict(t *testing.T) {
	db := setupTestDB(t)
	handler := handlePostDocs(db)

	// Insert
	body := `{"document_id":"doc1","title":"V1","content":"old"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	// Upsert same document
	body = `{"document_id":"doc1","title":"V2","content":"new"}`
	req = httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler(w, req)

	// Read — should have V2
	readHandler := handleReadDocs(db)
	req = httptest.NewRequest("GET", "/api/read?document_id=doc1", nil)
	w = httptest.NewRecorder()
	readHandler(w, req)

	var resp ReadResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 1 {
		t.Fatalf("expected 1 doc after upsert, got %d", resp.Count)
	}
	if *resp.Documents[0].Title != "V2" {
		t.Fatalf("expected title=V2, got %s", *resp.Documents[0].Title)
	}
}

func TestMultiTab(t *testing.T) {
	db := setupTestDB(t)
	handler := handlePostDocs(db)

	// Insert two tabs
	for _, tab := range []string{`{"document_id":"doc1","tab_id":"t.0","tab_name":"Tab A","content":"Content A"}`, `{"document_id":"doc1","tab_id":"t.1","tab_name":"Tab B","content":"Content B"}`} {
		req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(tab))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler(w, req)
		if w.Code != 200 {
			t.Fatalf("POST: status=%d", w.Code)
		}
	}

	readHandler := handleReadDocs(db)

	// Read all tabs for doc1
	req := httptest.NewRequest("GET", "/api/read?document_id=doc1", nil)
	w := httptest.NewRecorder()
	readHandler(w, req)

	var resp ReadResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Fatalf("expected 2 tabs, got %d", resp.Count)
	}

	// Read specific tab
	req = httptest.NewRequest("GET", "/api/read?document_id=doc1&tab_id=t.0", nil)
	w = httptest.NewRecorder()
	readHandler(w, req)

	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 1 {
		t.Fatalf("expected 1 tab, got %d", resp.Count)
	}
	if *resp.Documents[0].TabName != "Tab A" {
		t.Fatalf("expected Tab A, got %s", *resp.Documents[0].TabName)
	}
}

func TestSearch(t *testing.T) {
	db := setupTestDB(t)
	postHandler := handlePostDocs(db)

	// Insert docs
	for _, doc := range []string{
		`{"document_id":"d1","title":"Meeting Notes","content":"Discuss Q4 roadmap"}`,
		`{"document_id":"d2","title":"Design Doc","content":"Architecture overview"}`,
		`{"document_id":"d3","title":"Weekly Sync","content":"Meeting with the team"}`,
	} {
		req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(doc))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		postHandler(w, req)
	}

	searchHandler := handleSearchDocs(db)

	// Search for "meeting"
	req := httptest.NewRequest("GET", "/api/search?q=meeting", nil)
	w := httptest.NewRecorder()
	searchHandler(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /api/search: status=%d", w.Code)
	}

	var resp SearchResult
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 2 {
		t.Fatalf("expected 2 results for 'meeting', got %d", resp.Count)
	}
	if resp.Query != "meeting" {
		t.Fatalf("expected query=meeting, got %s", resp.Query)
	}
}

func TestSearchRequiresQuery(t *testing.T) {
	db := setupTestDB(t)
	searchHandler := handleSearchDocs(db)

	req := httptest.NewRequest("GET", "/api/search", nil)
	w := httptest.NewRecorder()
	searchHandler(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestReadWithTimeRange(t *testing.T) {
	db := setupTestDB(t)
	postHandler := handlePostDocs(db)

	body := `{"document_id":"doc1","title":"Test","content":"content"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	postHandler(w, req)

	readHandler := handleReadDocs(db)

	// Read with future "from" — should return nothing
	req = httptest.NewRequest("GET", "/api/read?from=2099-01-01", nil)
	w = httptest.NewRecorder()
	readHandler(w, req)

	var resp ReadResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 0 {
		t.Fatalf("expected 0 docs with future from, got %d", resp.Count)
	}
}

func TestReadMarkdown(t *testing.T) {
	db := setupTestDB(t)
	postHandler := handlePostDocs(db)

	body := `{"document_id":"doc1","title":"My Notes","content":"Hello world","url":"https://docs.google.com/document/d/doc1/edit"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	postHandler(w, req)

	mdHandler := handleReadMD(db)
	req = httptest.NewRequest("GET", "/api/read/md?document_id=doc1", nil)
	w = httptest.NewRecorder()
	mdHandler(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /api/read/md: status=%d", w.Code)
	}

	md := w.Body.String()
	if !bytes.Contains([]byte(md), []byte("My Notes")) {
		t.Fatalf("markdown missing title: %s", md)
	}
	if !bytes.Contains([]byte(md), []byte("Hello world")) {
		t.Fatalf("markdown missing content: %s", md)
	}
}

func TestAPIDocsEndpoint(t *testing.T) {
	tpl, _ := os.ReadFile("templates/api-docs.html")
	handler := handleAPIDocs(string(tpl))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /: status=%d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("Google Doc Radar")) {
		t.Fatalf("missing title in API docs page")
	}
}

func TestPostRequiresDocumentID(t *testing.T) {
	db := setupTestDB(t)
	handler := handlePostDocs(db)

	body := `{"title":"No ID"}`
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for missing document_id, got %d", w.Code)
	}
}

func TestTabNameFilter(t *testing.T) {
	db := setupTestDB(t)
	postHandler := handlePostDocs(db)

	for _, doc := range []string{
		`{"document_id":"d1","tab_id":"t.0","tab_name":"Meeting Notes","content":"A"}`,
		`{"document_id":"d1","tab_id":"t.1","tab_name":"Action Items","content":"B"}`,
	} {
		req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(doc))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		postHandler(w, req)
	}

	readHandler := handleReadDocs(db)
	req := httptest.NewRequest("GET", "/api/read?document_id=d1&tab_name=Meeting", nil)
	w := httptest.NewRecorder()
	readHandler(w, req)

	var resp ReadResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 1 {
		t.Fatalf("expected 1 result for tab_name filter, got %d", resp.Count)
	}
	if *resp.Documents[0].TabName != "Meeting Notes" {
		t.Fatalf("expected Meeting Notes, got %s", *resp.Documents[0].TabName)
	}
}
