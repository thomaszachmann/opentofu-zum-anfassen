package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lab-Werte, keine echten Secrets.
const (
	labKey   = "lab-api-key-123"
	labDBURL = "postgres://notes:lab-db-pw-123@db:5432/notes"
)

func TestLoadAPIKeyFromEnv(t *testing.T) {
	t.Setenv("NOTES_API_KEY_FILE", "")
	t.Setenv("NOTES_API_KEY", labKey)
	key, source, err := loadAPIKey()
	if err != nil || key != labKey || source != "env" {
		t.Fatalf("got %q, %q, %v", key, source, err)
	}
}

func TestLoadAPIKeyMissing(t *testing.T) {
	t.Setenv("NOTES_API_KEY_FILE", "")
	t.Setenv("NOTES_API_KEY", "")
	if _, _, err := loadAPIKey(); err == nil {
		t.Fatal("erwartet Fehler ohne Key")
	}
}

func TestLoadDBURLFilePreferred(t *testing.T) {
	f := filepath.Join(t.TempDir(), "db_url")
	if err := os.WriteFile(f, []byte(labDBURL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTES_DB_URL_FILE", f)
	t.Setenv("NOTES_DB_URL", "postgres://falsch")
	url, source, err := loadDBURL()
	if err != nil || url != labDBURL || source != "file" {
		t.Fatalf("got %q, %q, %v", url, source, err)
	}
}

func TestLoadDBURLUnsetMeansMemory(t *testing.T) {
	t.Setenv("NOTES_DB_URL_FILE", "")
	t.Setenv("NOTES_DB_URL", "")
	url, source, err := loadDBURL()
	if err != nil || url != "" || source != "" {
		t.Fatalf("got %q, %q, %v", url, source, err)
	}
}

func TestPostNotesRequiresKey(t *testing.T) {
	h := newMux(newMemStore(), labKey, newMetrics("memory"), nil)
	cases := []struct {
		key  string
		want int
	}{
		{"", http.StatusUnauthorized},
		{"falsch", http.StatusUnauthorized},
		{labKey, http.StatusCreated},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/notes",
			strings.NewReader(`{"text":"test"}`))
		req.Header.Set("X-API-Key", c.key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("key %q: status %d, want %d", c.key, rec.Code, c.want)
		}
	}
}

func TestMetrics(t *testing.T) {
	h := newMux(newMemStore(), labKey, newMetrics("memory"), nil)
	req := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("X-API-Key", labKey)
	h.ServeHTTP(httptest.NewRecorder(), req)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/gibtsnicht", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`notes_build_info{store="memory",version="dev"} 1`,
		`notes_http_requests_total{code="201",method="POST",path="/notes"} 1`,
		`notes_http_requests_total{code="404",method="GET",path="other"} 1`,
		`notes_created_total 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("fehlt: %s\n%s", want, body)
		}
	}
	if strings.Contains(body, "notes_db_up") {
		t.Error("notes_db_up nur im Postgres-Betrieb")
	}
}

// Integrationstest gegen echtes Postgres, nur wenn NOTES_TEST_DB_URL gesetzt ist:
//
//	NOTES_TEST_DB_URL=postgres://notes:lab-db-pw-123@127.0.0.1:5432/notes go test ./...
func TestPostgresStore(t *testing.T) {
	url := os.Getenv("NOTES_TEST_DB_URL")
	if url == "" {
		t.Skip("NOTES_TEST_DB_URL nicht gesetzt")
	}
	ctx := context.Background()
	db, err := openDB(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 2; i++ { // idempotent
		if err := migrate(ctx, db); err != nil {
			t.Fatalf("migrate %d: %v", i, err)
		}
	}
	st := &pgStore{db: db}
	n, err := st.Add(ctx, "integration")
	if err != nil {
		t.Fatal(err)
	}
	notes, err := st.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range notes {
		found = found || x.ID == n.ID
	}
	if !found {
		t.Fatalf("Notiz %s nicht gefunden", n.ID)
	}
}
