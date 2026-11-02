// oidc.go – Access Tokens von Keycloak prüfen (ab notes-api 0.4.0, „Keycloak zum Anfassen").
//
// Ist NOTES_OIDC_ISSUER gesetzt (z. B. http://kc.192.168.64.7.sslip.io:8080/realms/lab),
// verlangt POST /notes ein gültiges Bearer-Token mit der Realm-Rolle aus NOTES_OIDC_ROLE
// (Standard: notes-schreiben). NOTES_OIDC_AUDIENCE prüft zusätzlich den Claim aud.
// Geprüft werden Signatur (RS256, Schlüssel aus dem JWKS des Issuers), exp, iss und aud.
package main

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

type oidcConfig struct {
	issuer   string
	audience string
	role     string
	client   *http.Client

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

// claims enthält die Felder, die notes-api auswertet.
type claims struct {
	Iss               string          `json:"iss"`
	Sub               string          `json:"sub"`
	Exp               int64           `json:"exp"`
	Aud               json.RawMessage `json:"aud"`
	PreferredUsername string          `json:"preferred_username"`
	Email             string          `json:"email"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

func (c claims) audiences() []string {
	var one string
	if json.Unmarshal(c.Aud, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(c.Aud, &many)
	return many
}

// loadOIDC liefert nil, wenn NOTES_OIDC_ISSUER nicht gesetzt ist.
func loadOIDC() *oidcConfig {
	issuer := strings.TrimRight(os.Getenv("NOTES_OIDC_ISSUER"), "/")
	if issuer == "" {
		return nil
	}
	role := os.Getenv("NOTES_OIDC_ROLE")
	if role == "" {
		role = "notes-schreiben"
	}
	return &oidcConfig{
		issuer:   issuer,
		audience: os.Getenv("NOTES_OIDC_AUDIENCE"),
		role:     role,
		client:   &http.Client{Timeout: 5 * time.Second},
	}
}

var errNoToken = errors.New("kein Bearer-Token")

// verify prüft das Bearer-Token aus dem Authorization-Header.
func (o *oidcConfig) verify(ctx context.Context, header string) (claims, error) {
	var c claims
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return c, errNoToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, errors.New("kein JWT")
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodePart(parts[0], &head); err != nil {
		return c, fmt.Errorf("Header: %w", err)
	}
	if head.Alg != "RS256" {
		return c, fmt.Errorf("Algorithmus %q nicht erlaubt", head.Alg)
	}
	key, err := o.key(ctx, head.Kid)
	if err != nil {
		return c, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return c, errors.New("Signatur nicht lesbar")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return c, errors.New("Signatur ungültig")
	}
	if err := decodePart(parts[1], &c); err != nil {
		return c, fmt.Errorf("Payload: %w", err)
	}
	if c.Iss != o.issuer {
		return c, fmt.Errorf("falscher Issuer %q", c.Iss)
	}
	if time.Now().Unix() > c.Exp+30 {
		return c, errors.New("Token abgelaufen")
	}
	if o.audience != "" && !slices.Contains(c.audiences(), o.audience) {
		return c, fmt.Errorf("Audience %q fehlt", o.audience)
	}
	return c, nil
}

func decodePart(s string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// key liefert den öffentlichen Schlüssel zur kid; bei unbekannter kid wird der JWKS neu
// geladen (Schlüsselwechsel in Keycloak), höchstens alle 10 Sekunden.
func (o *oidcConfig) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if k, ok := o.keys[kid]; ok {
		return k, nil
	}
	if time.Since(o.fetched) < 10*time.Second {
		return nil, fmt.Errorf("unbekannte kid %q", kid)
	}
	keys, err := o.fetchJWKS(ctx)
	o.fetched = time.Now()
	if err != nil {
		return nil, fmt.Errorf("JWKS: %w", err)
	}
	o.keys = keys
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unbekannte kid %q", kid)
}

func (o *oidcConfig) getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (o *oidcConfig) fetchJWKS(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	var disc struct {
		JwksURI string `json:"jwks_uri"`
	}
	if err := o.getJSON(ctx, o.issuer+"/.well-known/openid-configuration", &disc); err != nil {
		return nil, err
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := o.getJSON(ctx, disc.JwksURI, &set); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n),
			E: int(new(big.Int).SetBytes(e).Int64())}
	}
	return keys, nil
}

// handleWhoami zeigt, was notes-api aus dem Token liest.
func handleWhoami(o *oidcConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if o == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "OIDC nicht aktiv"})
			return
		}
		c, err := o.verify(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user":  c.PreferredUsername,
			"email": c.Email,
			"roles": c.RealmAccess.Roles,
			"aud":   c.audiences(),
		})
	}
}
