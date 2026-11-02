// notes-api – kleines Notiz-API für das Trainingslager „Docker Compose zum Anfassen".
//
//	GET  /healthz        → ok (mit Datenbank: nur wenn sie antwortet, sonst 503)
//	GET  /version        → Version (per -ldflags "-X main.version=…")
//	GET  /notes          → alle Notizen als JSON
//	POST /notes          → neue Notiz {"text": "…"}, verlangt Header X-API-Key
//	                       (mit NOTES_OIDC_ISSUER stattdessen ein Bearer-Token, oidc.go)
//	GET  /whoami         → Inhalt des Bearer-Tokens (nur mit NOTES_OIDC_ISSUER)
//	GET  /upstream       → ruft NOTES_UPSTREAM per HTTPS auf (Test für CA-Zertifikate)
//	GET  /metrics        → Kennzahlen im Prometheus-Textformat
//
// Der API-Key kommt aus der Datei in NOTES_API_KEY_FILE oder aus NOTES_API_KEY.
// Ist NOTES_DB_URL_FILE oder NOTES_DB_URL gesetzt, speichert notes-api in Postgres,
// sonst im Speicher.
//
//	notes-api healthcheck  prüft /healthz (für HEALTHCHECK ohne curl)
//	notes-api migrate      legt die Tabelle notes an (idempotent) und endet
package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // Treiber "pgx" für database/sql
)

var version = "dev"

// readSecret liefert den Wert aus der Datei in <name>_FILE (bevorzugt) oder aus
// <name>. source ist "file", "env" oder "" (nicht gesetzt).
func readSecret(name string) (value, source string, err error) {
	if f := os.Getenv(name + "_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", "", fmt.Errorf("%s_FILE: %w", name, err)
		}
		return strings.TrimSpace(string(b)), "file", nil
	}
	if v := os.Getenv(name); v != "" {
		return v, "env", nil
	}
	return "", "", nil
}

func loadAPIKey() (key, source string, err error) {
	key, source, err = readSecret("NOTES_API_KEY")
	if err == nil && key == "" {
		err = errors.New("NOTES_API_KEY oder NOTES_API_KEY_FILE fehlt")
	}
	return key, source, err
}

// loadDBURL: leere URL heißt Speicherbetrieb.
func loadDBURL() (url, source string, err error) {
	return readSecret("NOTES_DB_URL")
}

func openDB(ctx context.Context, url string) (*sql.DB, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetConnMaxIdleTime(time.Minute)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// migrate legt das Schema an. Mehrfach ausführbar.
func migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS notes (
		id         uuid PRIMARY KEY,
		text       text NOT NULL,
		created_at timestamptz NOT NULL DEFAULT now()
	)`)
	return err
}

func healthcheck(addr string) int {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1" + addr + "/healthz")
	if err != nil {
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func runMigrate(log *slog.Logger) int {
	url, source, err := loadDBURL()
	if err == nil && url == "" {
		err = errors.New("NOTES_DB_URL oder NOTES_DB_URL_FILE fehlt")
	}
	if err != nil {
		log.Error("migration failed", "err", err)
		return 1
	}
	ctx := context.Background()
	db, err := openDB(ctx, url)
	if err != nil {
		log.Error("database unreachable", "source", source, "err", err)
		return 1
	}
	defer db.Close()
	if err := migrate(ctx, db); err != nil {
		log.Error("migration failed", "err", err)
		return 1
	}
	log.Info("migration complete", "table", "notes")
	return 0
}

func main() {
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(addr))
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(runMigrate(log))
	}

	apiKey, source, err := loadAPIKey()
	if err != nil {
		log.Error("start abgebrochen", "err", err)
		os.Exit(1)
	}
	// Nie den Key selbst loggen, nur woher er kam.
	log.Info("api key loaded", "source", source)

	var st store = newMemStore()
	dbURL, dbSource, err := loadDBURL()
	if err != nil {
		log.Error("start abgebrochen", "err", err)
		os.Exit(1)
	}
	if dbURL != "" {
		db, err := openDB(context.Background(), dbURL)
		if err != nil {
			// Die URL enthält das Passwort: nie loggen.
			log.Error("database unreachable", "source", dbSource, "err", err)
			os.Exit(1)
		}
		defer db.Close()
		st = &pgStore{db: db}
	}
	log.Info("store ready", "store", st.Kind())

	oidc := loadOIDC()
	if oidc != nil {
		log.Info("oidc enabled", "issuer", oidc.issuer, "role", oidc.role,
			"audience", oidc.audience)
	}
	m := newMetrics(st.Kind())
	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(st, apiKey, m, oidc),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("listening", "addr", addr, "version", version, "pid", os.Getpid())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("signal received, shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
	log.Info("shutdown complete")
}

func newMux(st store, apiKey string, m *metrics, oidc *oidcConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := st.Ping(ctx); err != nil {
			m.setDBUp(false)
			http.Error(w, "db unreachable", http.StatusServiceUnavailable)
			return
		}
		m.setDBUp(true)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, version)
	})
	mux.HandleFunc("/notes", handleNotes(st, apiKey, m, oidc))
	mux.HandleFunc("/whoami", handleWhoami(oidc))
	mux.HandleFunc("/upstream", handleUpstream)
	mux.HandleFunc("/metrics", m.handle)
	return m.middleware(mux)
}

func handleNotes(st store, apiKey string, m *metrics, oidc *oidcConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			notes, err := st.List(r.Context())
			if err != nil {
				slog.Error("list notes", "err", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storage"})
				return
			}
			writeJSON(w, http.StatusOK, notes)
		case http.MethodPost:
			if !authorized(w, r, apiKey, oidc) {
				return
			}
			var in struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Text == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text required"})
				return
			}
			n, err := st.Add(r.Context(), in.Text)
			if err != nil {
				slog.Error("add note", "err", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storage"})
				return
			}
			m.noteCreated()
			writeJSON(w, http.StatusCreated, n)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func handleUpstream(w http.ResponseWriter, r *http.Request) {
	url := os.Getenv("NOTES_UPSTREAM")
	if url == "" {
		url = "https://example.com"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	_ = resp.Body.Close()
	writeJSON(w, http.StatusOK, map[string]any{"url": url, "status": resp.StatusCode})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// authorized prüft API-Key oder, mit OIDC, Bearer-Token und Rolle. Bei false ist die
// Antwort schon geschrieben.
func authorized(w http.ResponseWriter, r *http.Request, apiKey string, oidc *oidcConfig) bool {
	if oidc == nil {
		got := r.Header.Get("X-API-Key")
		if subtle.ConstantTimeCompare([]byte(got), []byte(apiKey)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid api key"})
			return false
		}
		return true
	}
	c, err := oidc.verify(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return false
	}
	if !slices.Contains(c.RealmAccess.Roles, oidc.role) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Rolle " + oidc.role + " fehlt", "user": c.PreferredUsername})
		return false
	}
	return true
}
