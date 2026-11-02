package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testIssuer startet einen Server mit Discovery und JWKS für einen frischen RSA-Schlüssel.
func testIssuer(t *testing.T) (*httptest.Server, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"jwks_uri": srv.URL + "/certs"})
	})
	mux.HandleFunc("/certs", func(w http.ResponseWriter, _ *http.Request) {
		b64 := base64.RawURLEncoding.EncodeToString
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "k1", "kty": "RSA", "use": "sig",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, key
}

func sign(t *testing.T, key *rsa.PrivateKey, payload map[string]any) string {
	t.Helper()
	b64 := base64.RawURLEncoding.EncodeToString
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1"})
	body, _ := json.Marshal(payload)
	in := b64(head) + "." + b64(body)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return in + "." + b64(sig)
}

func TestOIDCPost(t *testing.T) {
	srv, key := testIssuer(t)
	o := &oidcConfig{issuer: srv.URL, audience: "notes-api", role: "notes-schreiben",
		client: srv.Client()}
	h := newMux(newMemStore(), labKey, newMetrics("memory"), o)
	base := map[string]any{"iss": srv.URL, "aud": "notes-api",
		"exp": time.Now().Add(time.Minute).Unix(), "preferred_username": "anna",
		"realm_access": map[string]any{"roles": []string{"notes-schreiben"}}}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"gültig", "Bearer " + sign(t, key, base), http.StatusCreated},
		{"ohne Rolle", "Bearer " + sign(t, key, with("realm_access",
			map[string]any{"roles": []string{"leser"}})), http.StatusForbidden},
		{"abgelaufen", "Bearer " + sign(t, key, with("exp",
			time.Now().Add(-time.Hour).Unix())), http.StatusUnauthorized},
		{"falsche Audience", "Bearer " + sign(t, key, with("aud", "andere")),
			http.StatusUnauthorized},
		{"manipuliert", "Bearer " + sign(t, key, base) + "x", http.StatusUnauthorized},
		{"ohne Token", "", http.StatusUnauthorized},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(`{"text":"x"}`))
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: Status %d, erwartet %d (%s)", c.name, rec.Code, c.want, rec.Body)
		}
	}
}
