package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestExporter(t *testing.T) (*mdExporter, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "md")
	exp, err := newMDExporter(dir)
	if err != nil {
		t.Fatalf("newMDExporter: %v", err)
	}
	if exp == nil {
		t.Fatal("expected exporter, got nil")
	}
	return exp, dir
}

func doPost(t *testing.T, exp *mdExporter, body string) map[string]interface{} {
	t.Helper()
	db := setupTestDB(t)
	handler := handlePostDocs(db, exp)
	req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != 200 {
		t.Fatalf("POST /api/docs: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, w.Body.String())
	}
	return resp
}

func TestMarkdownMirrorWritesFile(t *testing.T) {
	exp, dir := newTestExporter(t)
	body := `{"document_id":"doc1","tab_id":"t.0","tab_name":"Notes","title":"My Doc","content":"# My Doc\n\nHello world","url":"https://docs.google.com/document/d/doc1/edit"}`
	resp := doPost(t, exp, body)

	mdPath, _ := resp["markdown"].(string)
	if mdPath == "" {
		t.Fatalf("response missing markdown path: %v", resp)
	}
	if filepath.Dir(mdPath) != dir {
		t.Fatalf("markdown written outside configured dir: %s", mdPath)
	}

	raw, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read mirror file: %v", err)
	}
	md := string(raw)

	for _, want := range []string{
		`document_id: "doc1"`,
		`tab_id: "t.0"`,
		`tab_name: "Notes"`,
		`title: "My Doc"`,
		`url: "https://docs.google.com/document/d/doc1/edit"`,
		"updated_at: \"",
		"Hello world",
		"# My Doc",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("mirror file missing %q:\n%s", want, md)
		}
	}
	if !strings.HasPrefix(md, "---\n") {
		t.Fatalf("mirror file missing front matter:\n%s", md)
	}
}

func TestMarkdownMirrorOverwritesAndReplacesRenamedTitle(t *testing.T) {
	exp, dir := newTestExporter(t)
	db := setupTestDB(t)
	handler := handlePostDocs(db, exp)

	post := func(body string) {
		req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler(w, req)
		if w.Code != 200 {
			t.Fatalf("POST: status=%d body=%s", w.Code, w.Body.String())
		}
	}

	post(`{"document_id":"doc1","tab_id":"t.0","title":"Old Title","content":"v1"}`)
	post(`{"document_id":"doc1","tab_id":"t.0","title":"New Title","content":"v2"}`)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected exactly 1 file after rename, got %d: %v", len(entries), names)
	}
	if name := entries[0].Name(); !strings.HasPrefix(name, "New Title [doc1 - t.0]") {
		t.Fatalf("unexpected file name: %s", name)
	}

	raw, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !strings.Contains(string(raw), "v2") || strings.Contains(string(raw), "v1") {
		t.Fatalf("file was not overwritten:\n%s", raw)
	}
}

func TestMarkdownMirrorMultiTabSeparateFiles(t *testing.T) {
	exp, dir := newTestExporter(t)
	db := setupTestDB(t)
	handler := handlePostDocs(db, exp)

	for _, body := range []string{
		`{"document_id":"doc1","tab_id":"t.0","tab_name":"Tab A","title":"Doc","content":"A"}`,
		`{"document_id":"doc1","tab_id":"t.1","tab_name":"Tab B","title":"Doc","content":"B"}`,
	} {
		req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler(w, req)
		if w.Code != 200 {
			t.Fatalf("POST: status=%d body=%s", w.Code, w.Body.String())
		}
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("expected 2 files (one per tab), got %d", len(entries))
	}
}

func TestMarkdownMirrorDisabled(t *testing.T) {
	resp := doPost(t, nil, `{"document_id":"doc1","title":"No mirror","content":"x"}`)
	if v, ok := resp["markdown"]; !ok || v != nil {
		t.Fatalf("expected markdown=null when disabled, got %v", resp)
	}
}

func TestNewMDExporterEmptyDir(t *testing.T) {
	exp, err := newMDExporter("  ")
	if err != nil {
		t.Fatalf("newMDExporter: %v", err)
	}
	if exp != nil {
		t.Fatalf("expected nil exporter for empty dir, got %v", exp)
	}
}

func TestMarkdownBackfill(t *testing.T) {
	db := setupTestDB(t)
	handler := handlePostDocs(db, nil)
	for _, body := range []string{
		`{"document_id":"d1","title":"One","content":"a"}`,
		`{"document_id":"d2","tab_id":"t.0","title":"Two","content":"b"}`,
	} {
		req := httptest.NewRequest("POST", "/api/docs", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler(w, req)
	}

	exp, dir := newTestExporter(t)
	n, err := exp.Backfill(db)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 exported docs, got %d", n)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("expected 2 files after backfill, got %d", len(entries))
	}
}

func TestSanitizeFileStem(t *testing.T) {
	cases := map[string]string{
		"My Doc":                 "My Doc",
		"Q4 Planning: 规划":        "Q4 Planning - 规划",
		"a/b:c*d?e":              "a-b-c-de",
		`Quote "x" <y> |z|`:      `Quote 'x' y -z-`,
		"  . .  ":                "Untitled",
		"":                       "Untitled",
		"multi\nline\ttitle":     "multi line title",
		"多字节标题：测试 / 版本":          "多字节标题：测试 - 版本",
		strings.Repeat("x", 200): strings.Repeat("x", 100),
	}
	for in, want := range cases {
		if got := sanitizeFileStem(in); got != want {
			t.Errorf("sanitizeFileStem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileKeyOf(t *testing.T) {
	cases := map[string]string{
		"My Doc [doc1].md":       "doc1",
		"My Doc [doc1 - t.0].md": "doc1 - t.0",
		"a [b] c [doc1].md":      "doc1",
	}
	for in, want := range cases {
		got, ok := fileKeyOf(in)
		if !ok || got != want {
			t.Errorf("fileKeyOf(%q) = (%q, %v), want %q", in, got, ok, want)
		}
	}
	if _, ok := fileKeyOf("no-key.md"); ok {
		t.Error("expected no key for file without bracketed suffix")
	}
}
