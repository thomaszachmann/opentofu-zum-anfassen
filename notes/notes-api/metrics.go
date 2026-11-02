package main

// Kennzahlen im Prometheus-Textformat, ohne Bibliothek:
// https://prometheus.io/docs/instrumenting/exposition_formats/

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
)

type reqKey struct {
	method, path string
	code         int
}

type metrics struct {
	mu       sync.Mutex
	store    string
	requests map[reqKey]uint64
	created  uint64
	dbUp     int // -1 unbekannt, 0 down, 1 up
}

func newMetrics(store string) *metrics {
	return &metrics{store: store, requests: map[reqKey]uint64{}, dbUp: -1}
}

func (m *metrics) noteCreated() {
	m.mu.Lock()
	m.created++
	m.mu.Unlock()
}

func (m *metrics) setDBUp(up bool) {
	m.mu.Lock()
	m.dbUp = 0
	if up {
		m.dbUp = 1
	}
	m.mu.Unlock()
}

// Nur bekannte Pfade als Label – sonst wächst die Zahl der Zeitreihen unbegrenzt.
var knownPaths = map[string]bool{
	"/healthz": true, "/version": true, "/notes": true, "/upstream": true, "/metrics": true,
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func (m *metrics) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		path := r.URL.Path
		if !knownPaths[path] {
			path = "other"
		}
		m.mu.Lock()
		m.requests[reqKey{r.Method, path, rec.code}]++
		m.mu.Unlock()
	})
}

func (m *metrics) handle(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	fmt.Fprintln(w, "# HELP notes_build_info Version und Speicherart von notes-api.")
	fmt.Fprintln(w, "# TYPE notes_build_info gauge")
	fmt.Fprintf(w, "notes_build_info{store=%q,version=%q} 1\n", m.store, version)

	fmt.Fprintln(w, "# HELP notes_http_requests_total HTTP-Anfragen nach Methode, Pfad und Status.")
	fmt.Fprintln(w, "# TYPE notes_http_requests_total counter")
	keys := make([]reqKey, 0, len(m.requests))
	for k := range m.requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.path != b.path {
			return a.path < b.path
		}
		if a.method != b.method {
			return a.method < b.method
		}
		return a.code < b.code
	})
	for _, k := range keys {
		fmt.Fprintf(w, "notes_http_requests_total{code=\"%d\",method=%q,path=%q} %d\n",
			k.code, k.method, k.path, m.requests[k])
	}

	fmt.Fprintln(w, "# HELP notes_created_total Angelegte Notizen seit dem Start.")
	fmt.Fprintln(w, "# TYPE notes_created_total counter")
	fmt.Fprintf(w, "notes_created_total %d\n", m.created)

	if m.store == "postgres" && m.dbUp >= 0 {
		fmt.Fprintln(w, "# HELP notes_db_up 1, wenn der letzte Healthcheck die Datenbank erreicht hat.")
		fmt.Fprintln(w, "# TYPE notes_db_up gauge")
		fmt.Fprintf(w, "notes_db_up %d\n", m.dbUp)
	}
}
