package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ── Markdown mirror ──────────────────────────────────────────────────────────
//
// The DB stays the source of truth. When --md-dir is configured, every ingested
// document is additionally written to a Markdown file on disk, one file per
// (document_id, tab_id) — the same key the DB uses — so a re-ingest overwrites
// the file in place and a renamed document replaces its old file instead of
// leaving an orphan behind.

// mdExporter writes documents to a directory as Markdown files.
type mdExporter struct {
	dir string
}

// newMDExporter prepares dir. An empty dir disables the mirror and returns
// (nil, nil), so callers can treat "no exporter" as a valid state.
func newMDExporter(dir string) (*mdExporter, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create markdown dir %s: %w", dir, err)
	}
	// Fail fast at startup rather than on the first document ingest.
	probe, err := os.CreateTemp(dir, ".gdoc-radar-probe-*")
	if err != nil {
		return nil, fmt.Errorf("markdown dir %s not writable: %w", dir, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return &mdExporter{dir: dir}, nil
}

// Dir returns the configured output directory.
func (m *mdExporter) Dir() string {
	if m == nil {
		return ""
	}
	return m.dir
}

// mdKey is the stable identity embedded in the file name: the document ID plus,
// for multi-tab docs, the tab ID (not the tab name) so renaming a tab does not
// orphan the previous file.
func mdKey(documentID, tabID string) string {
	if tabID == "" {
		return documentID
	}
	return documentID + " - " + tabID
}

// fileName builds "<title> [<key>].md".
func (m *mdExporter) fileName(d ReadDoc) string {
	title := "Untitled"
	if d.Title != nil && strings.TrimSpace(*d.Title) != "" {
		title = *d.Title
	}
	tabID := ""
	if d.TabID != nil {
		tabID = *d.TabID
	}
	return sanitizeFileStem(title) + " [" + mdKey(d.DocumentID, tabID) + "].md"
}

// Write renders d and writes it atomically to the mirror directory, returning
// the file path. Stale files for the same key (older title) are removed.
func (m *mdExporter) Write(d ReadDoc) (string, error) {
	name := m.fileName(d)
	path := filepath.Join(m.dir, name)

	tmp, err := os.CreateTemp(m.dir, ".gdoc-radar-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(renderDocMarkdown(d)); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	// CreateTemp uses 0600; the mirror should be readable like a normal file.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", err
	}

	m.removeStale(d, name)
	return path, nil
}

// Backfill writes every document already in the DB to the mirror directory.
func (m *mdExporter) Backfill(db *sql.DB) (int, error) {
	docs, err := queryDocs(db, ReadQuery{Limit: 1 << 30})
	if err != nil {
		return 0, err
	}
	written := 0
	for _, d := range docs {
		if _, err := m.Write(d); err != nil {
			return written, fmt.Errorf("export %s: %w", d.DocumentID, err)
		}
		written++
	}
	return written, nil
}

// removeStale deletes earlier files that carry the same identity key but a
// different title — i.e. leftovers from a document rename.
func (m *mdExporter) removeStale(d ReadDoc, currentName string) {
	tabID := ""
	if d.TabID != nil {
		tabID = *d.TabID
	}
	key := mdKey(d.DocumentID, tabID)

	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == currentName || !strings.HasSuffix(name, ".md") {
			continue
		}
		if nameKey, ok := fileKeyOf(name); ok && nameKey == key {
			os.Remove(filepath.Join(m.dir, name))
		}
	}
}

// fileKeyOf extracts the "[<key>]" segment from a mirror file name.
func fileKeyOf(fileName string) (string, bool) {
	base := strings.TrimSuffix(fileName, ".md")
	if !strings.HasSuffix(base, "]") {
		return "", false
	}
	idx := strings.LastIndex(base, " [")
	if idx < 0 {
		return "", false
	}
	return base[idx+2 : len(base)-1], true
}

// renderDocMarkdown renders one document as YAML front matter + the document
// body. Google Docs markdown export already starts with a "# Title" heading, so
// no extra heading is added when content is present.
func renderDocMarkdown(d ReadDoc) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLField(&b, "document_id", d.DocumentID)
	if d.TabID != nil && *d.TabID != "" {
		writeYAMLField(&b, "tab_id", *d.TabID)
	}
	if d.TabName != nil && *d.TabName != "" {
		writeYAMLField(&b, "tab_name", *d.TabName)
	}
	title := "Untitled"
	if d.Title != nil && strings.TrimSpace(*d.Title) != "" {
		title = *d.Title
	}
	writeYAMLField(&b, "title", title)
	if d.URL != nil && *d.URL != "" {
		writeYAMLField(&b, "url", *d.URL)
	}
	if d.CreatedAt != nil && *d.CreatedAt != "" {
		writeYAMLField(&b, "created_at", *d.CreatedAt)
	}
	if d.UpdatedAt != nil && *d.UpdatedAt != "" {
		writeYAMLField(&b, "updated_at", *d.UpdatedAt)
	}
	writeYAMLField(&b, "source", "google-doc-radar")
	b.WriteString("---\n\n")

	content := ""
	if d.Content != nil {
		content = strings.TrimSpace(*d.Content)
	}
	if content == "" {
		fmt.Fprintf(&b, "# %s\n", title)
	} else {
		b.WriteString(content)
		b.WriteString("\n")
	}

	if d.URL != nil && *d.URL != "" {
		fmt.Fprintf(&b, "\n---\n\n[Open in Google Docs](%s)\n", *d.URL)
	}
	return b.String()
}

func writeYAMLField(b *strings.Builder, key, value string) {
	fmt.Fprintf(b, "%s: %s\n", key, yamlQuote(value))
}

// yamlQuote emits a double-quoted YAML scalar, escaping only what would
// otherwise break the file.
func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	return `"` + s + `"`
}

// sanitizeFileStem turns a title into a filesystem-safe file name stem.
func sanitizeFileStem(s string) string {
	// "Title: subtitle" reads better as "Title - subtitle" than "Title- subtitle".
	s = strings.ReplaceAll(s, ": ", " - ")
	replacer := strings.NewReplacer(
		"/", "-", `\`, "-", ":", "-", "*", "-",
		"?", "", `"`, "'", "<", "", ">", "", "|", "-",
	)
	s = replacer.Replace(s)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ") // collapse whitespace
	s = strings.Trim(s, " .")
	if s == "" {
		return "Untitled"
	}
	const maxRunes = 100
	if r := []rune(s); len(r) > maxRunes {
		s = strings.TrimSpace(string(r[:maxRunes]))
	}
	return s
}
