package testenv

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
	"net/url"
	"sync"
	"testing"
	"time"
)

// FakeIdP is a minimal OpenID Connect provider for tests: discovery, JWKS, an
// authorize endpoint that signs the configured user straight in, and a token
// endpoint that checks the client secret, redirect URI and PKCE before minting
// an RS256 ID token. It only listens on a local httptest server.
type FakeIdP struct {
	Server       *httptest.Server
	Issuer       string // what discovery and tokens say
	ClientID     string
	ClientSecret string
	// Tamper, when set, edits ID token claims just before signing (wrong aud, nonce...).
	Tamper func(map[string]any)

	mu     sync.Mutex
	key    *rsa.PrivateKey
	claims map[string]any
	codes  map[string]pending
}

type pending struct {
	nonce, challenge, redirect string
	claims                     map[string]any
}

// NewFakeIdP starts a provider. trailingSlash makes its issuer end in "/".
func NewFakeIdP(t testing.TB, trailingSlash bool) *FakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &FakeIdP{ClientID: "slidekick-test", ClientSecret: "client-secret-for-tests", key: key, codes: map[string]pending{}}
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Server.Close)
	f.Issuer = f.Server.URL
	if trailingSlash {
		f.Issuer += "/"
	}
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.Issuer, "authorization_endpoint": f.Server.URL + "/authorize", "token_endpoint": f.Server.URL + "/token",
			"jwks_uri": f.Server.URL + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		b64 := base64.RawURLEncoding
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": b64.EncodeToString(key.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != f.ClientID || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("nonce") == "" {
			http.Error(w, "bad authorize request", 400)
			return
		}
		code := randB64(16)
		f.mu.Lock()
		f.codes[code] = pending{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), claims: f.claims}
		f.mu.Unlock()
		u, _ := url.Parse(q.Get("redirect_uri"))
		v := u.Query()
		v.Set("code", code)
		v.Set("state", q.Get("state"))
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		id, sec, ok := r.BasicAuth()
		if !ok {
			id, sec = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		if id != f.ClientID || sec != f.ClientSecret {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return
		}
		f.mu.Lock()
		p, ok := f.codes[r.PostForm.Get("code")]
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		reason := ""
		switch {
		case !ok:
			reason = "unknown code"
		case base64.RawURLEncoding.EncodeToString(sum[:]) != p.challenge:
			reason = "PKCE verifier doesn't match the challenge"
		case r.PostForm.Get("redirect_uri") != p.redirect:
			reason = "redirect_uri " + r.PostForm.Get("redirect_uri") + " != " + p.redirect
		}
		if reason != "" {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": reason})
			return
		}
		f.mu.Lock()
		delete(f.codes, r.PostForm.Get("code")) // single use, once redeemed
		f.mu.Unlock()
		claims := map[string]any{"iss": f.Issuer, "aud": f.ClientID, "nonce": p.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()}
		for k, v := range p.claims {
			claims[k] = v
		}
		if f.Tamper != nil {
			f.Tamper(claims)
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + randB64(8), "token_type": "Bearer", "expires_in": 300, "id_token": f.sign(claims)})
	})
	return f
}

// SignInAs sets the claims (sub, email, email_verified, groups, name...) of the next sign-ins.
func (f *FakeIdP) SignInAs(claims map[string]any) {
	f.mu.Lock()
	f.claims = claims
	f.mu.Unlock()
}

func (f *FakeIdP) sign(claims map[string]any) string {
	b64 := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	c, _ := json.Marshal(claims)
	input := b64.EncodeToString(h) + "." + b64.EncodeToString(c)
	sum := sha256.Sum256([]byte(input))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	return input + "." + b64.EncodeToString(sig)
}

func randB64(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
