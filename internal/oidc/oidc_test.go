package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testClientID     = "slidekick-test"
	testClientSecret = "s3cret-for-tests"
	testRedirect     = "https://slidekick.example.test/auth/callback"
	testCode         = "the-one-valid-code"
)

// fakeIdP is a minimal OpenID provider: discovery, JWKS and token endpoint.
type fakeIdP struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey // published in the JWKS

	mu              sync.Mutex
	discoveryIssuer string          // "issuer" in the discovery doc
	tokenIssuer     string          // "iss" in minted tokens; defaults to discoveryIssuer
	challenge       string          // code_challenge the test copied from the auth URL
	nonce           string          // nonce to put in the ID token
	audience        string          // defaults to testClientID
	expiry          time.Time       // defaults to now+5m
	signKey         *rsa.PrivateKey // defaults to key
	extraClaims     map[string]any

	discoveryHits atomic.Int64
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", f.discovery)
	mux.HandleFunc("GET /jwks", f.jwks)
	mux.HandleFunc("POST /token", f.token)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.discoveryIssuer = f.srv.URL
	return f
}

func (f *fakeIdP) set(fn func(*fakeIdP)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeIdP) discovery(w http.ResponseWriter, r *http.Request) {
	f.discoveryHits.Add(1)
	f.mu.Lock()
	iss := f.discoveryIssuer
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                f.srv.URL + "/authorize",
		"token_endpoint":                        f.srv.URL + "/token",
		"jwks_uri":                              f.srv.URL + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (f *fakeIdP) jwks(w http.ResponseWriter, r *http.Request) {
	pub := f.key.PublicKey
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
		"n": b64(pub.N.Bytes()),
		"e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func (f *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthErr(w, "invalid_request")
		return
	}
	id, secret, ok := r.BasicAuth()
	if ok {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != testClientID || secret != testClientSecret {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("code") != testCode {
		oauthErr(w, "invalid_grant")
		return
	}
	if r.PostForm.Get("redirect_uri") != testRedirect {
		oauthErr(w, "invalid_grant")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if f.challenge == "" || subtle.ConstantTimeCompare([]byte(b64(sum[:])), []byte(f.challenge)) != 1 {
		oauthErr(w, "invalid_grant")
		return
	}
	iss := f.tokenIssuer
	if iss == "" {
		iss = f.discoveryIssuer
	}
	aud := f.audience
	if aud == "" {
		aud = testClientID
	}
	exp := f.expiry
	if exp.IsZero() {
		exp = time.Now().Add(5 * time.Minute)
	}
	claims := map[string]any{
		"iss": iss, "sub": "user-123", "aud": aud,
		"exp": exp.Unix(), "iat": time.Now().Add(-time.Minute).Unix(),
		"nonce": f.nonce,
	}
	for k, v := range f.extraClaims {
		claims[k] = v
	}
	signKey := f.signKey
	if signKey == nil {
		signKey = f.key
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": "at-xyz",
		"token_type":   "Bearer",
		"expires_in":   300,
		"id_token":     signJWT(f.t, signKey, claims),
	})
}

func oauthErr(w http.ResponseWriter, code string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func signJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signing := b64(hdr) + "." + b64(body)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + b64(sig)
}

func (f *fakeIdP) config() ProviderConfig {
	return ProviderConfig{
		ID: "fake", Name: "Fake IdP", Issuer: f.srv.URL,
		ClientID: testClientID, ClientSecret: testClientSecret,
	}
}

// begin runs Begin and hands the PKCE challenge and nonce to the fake, the way
// a real browser round trip through /authorize would.
func begin(t *testing.T, m *Manager, f *fakeIdP, p ProviderConfig) (url.Values, Flow) {
	t.Helper()
	authURL, flow, err := m.Begin(context.Background(), p, testRedirect)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	f.set(func(f *fakeIdP) {
		f.challenge = q.Get("code_challenge")
		f.nonce = q.Get("nonce")
	})
	return q, flow
}

func signIn(t *testing.T, m *Manager, f *fakeIdP, p ProviderConfig) (Identity, error) {
	t.Helper()
	_, flow := begin(t, m, f, p)
	return m.Finish(context.Background(), p, testRedirect, flow, flow.State, testCode)
}

func TestHappyPathGroupsArray(t *testing.T) {
	f := newFakeIdP(t)
	f.set(func(f *fakeIdP) {
		f.extraClaims = map[string]any{
			"email": "a@example.test", "email_verified": true, "name": "Ada Example",
			"groups": []string{"admins", "editors"},
		}
	})
	id, err := signIn(t, NewManager(nil), f, f.config())
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	want := Identity{Issuer: f.srv.URL, Subject: "user-123", Email: "a@example.test", EmailVerified: true,
		Name: "Ada Example", Groups: []string{"admins", "editors"}}
	if id.Issuer != want.Issuer || id.Subject != want.Subject || id.Email != want.Email ||
		id.EmailVerified != want.EmailVerified || id.Name != want.Name || !slices.Equal(id.Groups, want.Groups) {
		t.Fatalf("identity = %+v, want %+v", id, want)
	}
}

func TestHappyPathGroupsStringCustomClaimNoEmailVerified(t *testing.T) {
	f := newFakeIdP(t)
	f.set(func(f *fakeIdP) {
		f.extraClaims = map[string]any{"email": "b@example.test", "roles": "slidekick-admin", "groups": []string{"ignored"}}
	})
	p := f.config()
	p.GroupsClaim = "roles"
	id, err := signIn(t, NewManager(nil), f, p)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if !slices.Equal(id.Groups, []string{"slidekick-admin"}) {
		t.Fatalf("groups = %v", id.Groups)
	}
	if id.EmailVerified {
		t.Fatal("EmailVerified should be false when the claim is absent")
	}
}

func TestStringEmailVerified(t *testing.T) {
	f := newFakeIdP(t)
	f.set(func(f *fakeIdP) { f.extraClaims = map[string]any{"email_verified": "true"} })
	id, err := signIn(t, NewManager(nil), f, f.config())
	if err != nil || !id.EmailVerified {
		t.Fatalf("id=%+v err=%v", id, err)
	}
}

func TestBadGroupsClaimRejected(t *testing.T) {
	f := newFakeIdP(t)
	f.set(func(f *fakeIdP) { f.extraClaims = map[string]any{"groups": []any{"a", 3}} })
	if _, err := signIn(t, NewManager(nil), f, f.config()); err == nil {
		t.Fatal("expected error for non-string group")
	}
}

func TestStateMismatch(t *testing.T) {
	f := newFakeIdP(t)
	m := NewManager(nil)
	_, flow := begin(t, m, f, f.config())
	_, err := m.Finish(context.Background(), f.config(), testRedirect, flow, flow.State+"x", testCode)
	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("err = %v, want ErrStateMismatch", err)
	}
	_, err = m.Finish(context.Background(), f.config(), testRedirect, flow, "", testCode)
	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("empty state: err = %v", err)
	}
}

func TestExpiredFlow(t *testing.T) {
	f := newFakeIdP(t)
	m := NewManager(nil)
	_, flow := begin(t, m, f, f.config())
	flow.Created = time.Now().Add(-FlowMaxAge - time.Second)
	_, err := m.Finish(context.Background(), f.config(), testRedirect, flow, flow.State, testCode)
	if !errors.Is(err, ErrFlowExpired) {
		t.Fatalf("err = %v, want ErrFlowExpired", err)
	}
}

func TestFlowForOtherProvider(t *testing.T) {
	f := newFakeIdP(t)
	m := NewManager(nil)
	_, flow := begin(t, m, f, f.config())
	flow.ProviderID = "other"
	if _, err := m.Finish(context.Background(), f.config(), testRedirect, flow, flow.State, testCode); err == nil {
		t.Fatal("expected error for flow of another provider")
	}
}

func TestNonceMismatch(t *testing.T) {
	f := newFakeIdP(t)
	m := NewManager(nil)
	_, flow := begin(t, m, f, f.config())
	f.set(func(f *fakeIdP) { f.nonce = "attacker-nonce" })
	_, err := m.Finish(context.Background(), f.config(), testRedirect, flow, flow.State, testCode)
	if !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("err = %v, want ErrNonceMismatch", err)
	}
}

func TestTokenRejections(t *testing.T) {
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		mod  func(*fakeIdP)
		want string
	}{
		{"wrong audience", func(f *fakeIdP) { f.audience = "someone-else" }, "audience"},
		{"wrong issuer in token", func(f *fakeIdP) { f.tokenIssuer = "https://evil.example.test" }, "issued by a different provider"},
		{"expired token", func(f *fakeIdP) { f.expiry = time.Now().Add(-time.Minute) }, "expired"},
		{"bad signature", func(f *fakeIdP) { f.signKey = otherKey }, "signature"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeIdP(t)
			f.set(tc.mod)
			_, err := signIn(t, NewManager(nil), f, f.config())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestWrongPKCEVerifier(t *testing.T) {
	f := newFakeIdP(t)
	m := NewManager(nil)
	_, flow := begin(t, m, f, f.config())
	flow.Verifier = strings.Repeat("A", 43)
	_, err := m.Finish(context.Background(), f.config(), testRedirect, flow, flow.State, testCode)
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("err = %v, want invalid_grant from token endpoint", err)
	}
}

func TestWrongClientSecret(t *testing.T) {
	f := newFakeIdP(t)
	p := f.config()
	p.ClientSecret = "nope"
	if _, err := signIn(t, NewManager(nil), f, p); err == nil {
		t.Fatal("expected exchange to fail with wrong secret")
	}
}

func TestTrailingSlashConfiguredDiscoveredWithout(t *testing.T) {
	f := newFakeIdP(t) // discovery issuer = srv.URL (no slash)
	p := f.config()
	p.Issuer = f.srv.URL + "/"
	m := NewManager(nil)
	got, err := m.Check(context.Background(), p)
	if err != nil || got != f.srv.URL {
		t.Fatalf("Check = %q, %v", got, err)
	}
	id, err := signIn(t, m, f, p)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if id.Issuer != f.srv.URL {
		t.Fatalf("issuer = %q", id.Issuer)
	}
	// Tokens are checked against the exact discovered issuer, not the typed one.
	f.set(func(f *fakeIdP) { f.tokenIssuer = f.srv.URL + "/" })
	if _, err := signIn(t, m, f, p); err == nil {
		t.Fatal("token with the configured (slash) issuer should be rejected")
	}
}

func TestTrailingSlashDiscoveredWithConfiguredWithout(t *testing.T) {
	f := newFakeIdP(t)
	f.set(func(f *fakeIdP) { f.discoveryIssuer = f.srv.URL + "/" })
	m := NewManager(nil)
	p := f.config() // Issuer = srv.URL (no slash)
	got, err := m.Check(context.Background(), p)
	if err != nil || got != f.srv.URL+"/" {
		t.Fatalf("Check = %q, %v", got, err)
	}
	id, err := signIn(t, m, f, p)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if id.Issuer != f.srv.URL+"/" {
		t.Fatalf("issuer = %q", id.Issuer)
	}
}

func TestIssuerMismatch(t *testing.T) {
	for _, disc := range []string{"/tenant-b", "//"} {
		f := newFakeIdP(t)
		f.set(func(f *fakeIdP) { f.discoveryIssuer = f.srv.URL + disc })
		_, err := NewManager(nil).Check(context.Background(), f.config())
		if !errors.Is(err, ErrIssuerMismatch) {
			t.Fatalf("%s: err = %v, want ErrIssuerMismatch", disc, err)
		}
		if !strings.Contains(err.Error(), f.srv.URL+disc) || !strings.Contains(err.Error(), `"`+f.srv.URL+`"`) {
			t.Fatalf("error should name both issuers: %v", err)
		}
		if _, _, err := NewManager(nil).Begin(context.Background(), f.config(), testRedirect); !errors.Is(err, ErrIssuerMismatch) {
			t.Fatalf("Begin err = %v", err)
		}
	}
}

func TestMultiTenantRejected(t *testing.T) {
	f := newFakeIdP(t)
	f.set(func(f *fakeIdP) { f.discoveryIssuer = "https://login.microsoftonline.com/{tenantid}/v2.0" })
	_, err := NewManager(nil).Check(context.Background(), f.config())
	if !errors.Is(err, ErrMultiTenant) {
		t.Fatalf("err = %v, want ErrMultiTenant", err)
	}
}

func TestParseCallback(t *testing.T) {
	_, _, err := ParseCallback(url.Values{"error": {"access_denied"}, "error_description": {"user said no"}, "state": {"s"}})
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Code != "access_denied" || pe.Description != "user said no" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("message = %q", err.Error())
	}
	if _, _, err := ParseCallback(url.Values{"state": {"s"}}); err == nil {
		t.Fatal("missing code should be an error")
	}
	st, code, err := ParseCallback(url.Values{"state": {"s"}, "code": {"c"}})
	if err != nil || st != "s" || code != "c" {
		t.Fatalf("got %q %q %v", st, code, err)
	}
}

func TestBeginURL(t *testing.T) {
	f := newFakeIdP(t)
	p := f.config()
	p.Scopes = []string{"offline_access", "openid", "groups extra"}
	m := NewManager(nil)
	authURL, flow, err := m.Begin(context.Background(), p, testRedirect)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	if u.Scheme+"://"+u.Host+u.Path != f.srv.URL+"/authorize" {
		t.Fatalf("auth endpoint = %s", authURL)
	}
	q := u.Query()
	sum := sha256.Sum256([]byte(flow.Verifier))
	checks := map[string]string{
		"response_type":         "code",
		"client_id":             testClientID,
		"redirect_uri":          testRedirect,
		"state":                 flow.State,
		"nonce":                 flow.Nonce,
		"code_challenge_method": "S256",
		"code_challenge":        b64(sum[:]),
		"scope":                 "openid email profile offline_access groups extra",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if q.Has("code_verifier") || strings.Contains(authURL, flow.Verifier) {
		t.Error("verifier leaked into the auth URL")
	}
	for name, v := range map[string]string{"state": flow.State, "nonce": flow.Nonce, "verifier": flow.Verifier} {
		raw, err := base64.RawURLEncoding.DecodeString(v)
		if err != nil || len(raw) < 32 {
			t.Errorf("%s %q is not >=32 bytes of base64url", name, v)
		}
	}
	if flow.State == flow.Nonce || flow.State == flow.Verifier || flow.ProviderID != "fake" || flow.Created.IsZero() {
		t.Errorf("bad flow %+v", flow)
	}
	_, flow2, _ := m.Begin(context.Background(), p, testRedirect)
	if flow2.State == flow.State || flow2.Nonce == flow.Nonce || flow2.Verifier == flow.Verifier {
		t.Error("random values repeat across flows")
	}
}

func TestDiscoveryCache(t *testing.T) {
	f := newFakeIdP(t)
	m := NewManager(nil)
	now := time.Now()
	m.now = func() time.Time { return now }
	p := f.config()
	for range 3 {
		if _, err := signIn(t, m, f, p); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.discoveryHits.Load(); n != 1 {
		t.Fatalf("discovery hits = %d, want 1", n)
	}
	// Same issuer typed with a slash shares the entry.
	p2 := p
	p2.Issuer += "/"
	if _, _, err := m.Begin(context.Background(), p2, testRedirect); err != nil {
		t.Fatal(err)
	}
	if n := f.discoveryHits.Load(); n != 1 {
		t.Fatalf("discovery hits = %d after slash variant, want 1", n)
	}
	// Another client ID is a separate entry.
	p3 := p
	p3.ClientID = "other-client"
	if _, _, err := m.Begin(context.Background(), p3, testRedirect); err != nil {
		t.Fatal(err)
	}
	if n := f.discoveryHits.Load(); n != 2 {
		t.Fatalf("discovery hits = %d after other client, want 2", n)
	}
	// After the TTL, discovery runs again.
	now = now.Add(DiscoveryTTL + time.Second)
	if _, _, err := m.Begin(context.Background(), p, testRedirect); err != nil {
		t.Fatal(err)
	}
	if n := f.discoveryHits.Load(); n != 3 {
		t.Fatalf("discovery hits = %d after TTL, want 3", n)
	}
}

func TestDiscoveryFailureNotCached(t *testing.T) {
	f := newFakeIdP(t)
	f.set(func(f *fakeIdP) { f.discoveryIssuer = "https://wrong.example.test" })
	m := NewManager(nil)
	if _, _, err := m.Begin(context.Background(), f.config(), testRedirect); err == nil {
		t.Fatal("expected mismatch")
	}
	f.set(func(f *fakeIdP) { f.discoveryIssuer = f.srv.URL })
	if _, err := signIn(t, m, f, f.config()); err != nil {
		t.Fatalf("after fix: %v", err)
	}
}

func TestRedirectToOtherSchemeRefused(t *testing.T) {
	target := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(target.Close)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redir.Close)
	_, err := NewManager(nil).Check(context.Background(), ProviderConfig{Issuer: redir.URL})
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	m := NewManager(nil)
	for _, iss := range []string{"", "ftp://x", "not a url", "https://x/?q=1"} {
		if _, err := m.Check(context.Background(), ProviderConfig{Issuer: iss}); err == nil {
			t.Errorf("issuer %q accepted", iss)
		}
	}
	if _, _, err := m.Begin(context.Background(), ProviderConfig{Issuer: "https://x"}, testRedirect); err == nil {
		t.Error("missing client ID accepted")
	}
}
