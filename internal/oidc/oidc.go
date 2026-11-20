// Package oidc implements OpenID Connect sign-in (authorization code flow with
// PKCE) against admin-configured identity providers such as Entra ID, Google,
// Authentik and Keycloak.
//
// The package is deliberately stateless towards the browser: Begin returns a
// Flow that the caller stores server-side (bound to the browser with a cookie)
// and hands back to Finish on the callback. Discovery documents are cached per
// issuer and client for DiscoveryTTL.
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	// DiscoveryTTL is how long a discovery document (and the JWKS key set
	// that hangs off it) is reused before discovery runs again.
	DiscoveryTTL = time.Hour
	// FlowMaxAge is the longest a sign-in may take between Begin and Finish.
	FlowMaxAge = 10 * time.Minute
	// DefaultGroupsClaim is used when ProviderConfig.GroupsClaim is empty.
	DefaultGroupsClaim = "groups"

	defaultHTTPTimeout = 10 * time.Second
	maxDiscoveryBytes  = 1 << 20
	randomBytes        = 32
)

// baseScopes are always requested, before any ProviderConfig.Scopes.
var baseScopes = []string{gooidc.ScopeOpenID, "email", "profile"}

// supportedAlgs mirrors the signing algorithms go-oidc can verify; anything
// else a provider advertises is ignored.
var supportedAlgs = map[string]bool{
	"RS256": true, "RS384": true, "RS512": true,
	"ES256": true, "ES384": true, "ES512": true,
	"PS256": true, "PS384": true, "PS512": true,
	"EdDSA": true,
}

// ProviderConfig is one identity provider an admin configured (Entra, Google, Authentik, Keycloak...).
type ProviderConfig struct {
	ID           string // short slug used in URLs, e.g. "entra"
	Name         string // button label
	Issuer       string // as the admin typed it (with or without trailing slash)
	ClientID     string
	ClientSecret string   // plaintext here; the caller decrypts it
	Scopes       []string // extra scopes; "openid email profile" are always requested
	GroupsClaim  string   // claim holding groups/roles, default "groups"
}

// Identity is what a successful sign-in proves.
type Identity struct {
	Issuer, Subject string
	Email           string
	EmailVerified   bool // false when the claim is absent
	Name            string
	Groups          []string // from GroupsClaim; accept a JSON array of strings or a single string
}

// Flow is the per-login state the caller stores server-side (and binds to the
// browser with a cookie) between Begin and Finish. Verifier is the PKCE code
// verifier and must never reach the browser.
type Flow struct {
	State, Nonce, Verifier, ProviderID string
	Created                            time.Time
}

// ProviderError is the error a provider reported on the callback URL
// (error, error_description, error_uri parameters).
type ProviderError struct {
	Code        string
	Description string
	URI         string
}

func (e *ProviderError) Error() string {
	msg := "oidc: identity provider returned error " + e.Code
	if e.Description != "" {
		msg += ": " + e.Description
	}
	return msg
}

// Sentinel errors callers may want to tell apart (all wrapped, use errors.Is).
var (
	ErrStateMismatch  = errors.New("oidc: state mismatch")
	ErrFlowExpired    = errors.New("oidc: sign-in flow expired")
	ErrNonceMismatch  = errors.New("oidc: nonce mismatch")
	ErrIssuerMismatch = errors.New("oidc: issuer mismatch")
	ErrMultiTenant    = errors.New("oidc: multi-tenant issuer templates are not supported")
)

// ParseCallback extracts state and code from the query of the redirect back
// from the provider. When the provider reported an error (error=...), it
// returns a *ProviderError instead. Callers should run this on the callback
// request and pass state and code on to Finish.
func ParseCallback(q url.Values) (state, code string, err error) {
	if e := q.Get("error"); e != "" {
		return "", "", &ProviderError{
			Code:        e,
			Description: q.Get("error_description"),
			URI:         q.Get("error_uri"),
		}
	}
	state, code = q.Get("state"), q.Get("code")
	if code == "" {
		return "", "", errors.New("oidc: callback carries no authorization code")
	}
	return state, code, nil
}

type cacheEntry struct {
	provider *gooidc.Provider
	issuer   string // exact issuer from the discovery document
	expires  time.Time
}

// Manager runs sign-in flows. It is safe for concurrent use.
type Manager struct {
	client *http.Client
	ttl    time.Duration
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewManager returns a Manager using client for every request to identity
// providers. A nil client gets a 10s timeout and refuses redirects that change
// the URL scheme (e.g. https to http).
func NewManager(client *http.Client) *Manager {
	if client == nil {
		client = &http.Client{
			Timeout: defaultHTTPTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("oidc: too many redirects")
				}
				if req.URL.Scheme != via[0].URL.Scheme {
					return fmt.Errorf("oidc: refusing redirect from %s to %s", via[0].URL.Scheme, req.URL.Scheme)
				}
				return nil
			},
		}
	}
	return &Manager{
		client: client,
		ttl:    DiscoveryTTL,
		now:    time.Now,
		cache:  make(map[string]cacheEntry),
	}
}

// Check performs discovery only, for the admin settings "test" button; returns
// the discovered issuer. It bypasses the cache (and refreshes it on success).
func (m *Manager) Check(ctx context.Context, p ProviderConfig) (string, error) {
	if err := validateConfig(p, false); err != nil {
		return "", err
	}
	e, err := m.discover(ctx, p)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.cache[cacheKey(p)] = e
	m.mu.Unlock()
	return e.issuer, nil
}

// Begin returns the authorization URL (authorization code flow, PKCE S256,
// state, nonce, response_type=code) and the Flow to store.
func (m *Manager) Begin(ctx context.Context, p ProviderConfig, redirectURL string) (string, Flow, error) {
	if err := validateConfig(p, true); err != nil {
		return "", Flow{}, err
	}
	if redirectURL == "" {
		return "", Flow{}, errors.New("oidc: redirect URL is required")
	}
	e, err := m.provider(ctx, p)
	if err != nil {
		return "", Flow{}, err
	}
	f := Flow{ProviderID: p.ID, Created: m.now()}
	for _, dst := range []*string{&f.State, &f.Nonce, &f.Verifier} {
		if *dst, err = randomToken(); err != nil {
			return "", Flow{}, err
		}
	}
	cfg := oauthConfig(p, e.provider, redirectURL)
	authURL := cfg.AuthCodeURL(f.State,
		gooidc.Nonce(f.Nonce),
		oauth2.S256ChallengeOption(f.Verifier),
	)
	return authURL, f, nil
}

// Finish validates the callback and returns the proven identity: gotState must
// equal f.State (constant-time), the flow must be younger than FlowMaxAge, the
// code is exchanged with the PKCE verifier, and the ID token is verified
// (signature via JWKS, issuer, audience = ClientID, expiry, nonce == f.Nonce).
// Provider error parameters on the callback are handled by ParseCallback.
func (m *Manager) Finish(ctx context.Context, p ProviderConfig, redirectURL string, f Flow, gotState, code string) (Identity, error) {
	if err := validateConfig(p, true); err != nil {
		return Identity{}, err
	}
	if f.State == "" || subtle.ConstantTimeCompare([]byte(gotState), []byte(f.State)) != 1 {
		return Identity{}, ErrStateMismatch
	}
	if f.ProviderID != p.ID {
		return Identity{}, fmt.Errorf("oidc: flow was started for provider %q, callback is for %q", f.ProviderID, p.ID)
	}
	age := m.now().Sub(f.Created)
	if f.Created.IsZero() || age < 0 || age >= FlowMaxAge {
		return Identity{}, ErrFlowExpired
	}
	if code == "" {
		return Identity{}, errors.New("oidc: authorization code is empty")
	}
	if f.Nonce == "" || f.Verifier == "" {
		return Identity{}, errors.New("oidc: flow is missing nonce or verifier")
	}

	e, err := m.provider(ctx, p)
	if err != nil {
		return Identity{}, err
	}
	cfg := oauthConfig(p, e.provider, redirectURL)
	tok, err := cfg.Exchange(m.clientCtx(ctx), code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("oidc: code exchange failed: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return Identity{}, errors.New("oidc: token response carries no id_token")
	}
	verifier := e.provider.VerifierContext(m.clientCtx(ctx), &gooidc.Config{ClientID: p.ClientID, Now: m.now})
	idt, err := verifier.Verify(m.clientCtx(ctx), rawID)
	if err != nil {
		return Identity{}, fmt.Errorf("oidc: id token rejected: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(f.Nonce)) != 1 {
		return Identity{}, ErrNonceMismatch
	}
	return identityFromToken(idt, p)
}

func identityFromToken(idt *gooidc.IDToken, p ProviderConfig) (Identity, error) {
	var claims map[string]json.RawMessage
	if err := idt.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("oidc: decoding id token claims: %w", err)
	}
	id := Identity{Issuer: idt.Issuer, Subject: idt.Subject}
	if idt.Subject == "" {
		return Identity{}, errors.New("oidc: id token has no subject")
	}
	if err := optString(claims, "email", &id.Email); err != nil {
		return Identity{}, err
	}
	if err := optString(claims, "name", &id.Name); err != nil {
		return Identity{}, err
	}
	if raw, ok := claims["email_verified"]; ok {
		v, err := parseBool(raw)
		if err != nil {
			return Identity{}, fmt.Errorf("oidc: claim email_verified: %w", err)
		}
		id.EmailVerified = v
	}
	gc := p.GroupsClaim
	if gc == "" {
		gc = DefaultGroupsClaim
	}
	if raw, ok := claims[gc]; ok {
		g, err := parseGroups(raw)
		if err != nil {
			return Identity{}, fmt.Errorf("oidc: claim %s: %w", gc, err)
		}
		id.Groups = g
	}
	return id, nil
}

func optString(claims map[string]json.RawMessage, name string, dst *string) error {
	raw, ok := claims[name]
	if !ok || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("oidc: claim %s is not a string", name)
	}
	return nil
}

// parseBool accepts true/false and, because some providers send it that way,
// the strings "true"/"false".
func parseBool(raw json.RawMessage) (bool, error) {
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(s) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	if string(raw) == "null" {
		return false, nil
	}
	return false, fmt.Errorf("not a boolean: %s", raw)
}

// parseGroups accepts a JSON array of strings or a single string.
func parseGroups(raw json.RawMessage) ([]string, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		if one == "" {
			return nil, nil
		}
		return []string{one}, nil
	}
	return nil, errors.New("expected a string or an array of strings")
}

func oauthConfig(p ProviderConfig, prov *gooidc.Provider, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		Endpoint:     prov.Endpoint(),
		RedirectURL:  redirectURL,
		Scopes:       scopes(p.Scopes),
	}
}

func scopes(extra []string) []string {
	out := append([]string(nil), baseScopes...)
	seen := map[string]bool{}
	for _, s := range out {
		seen[s] = true
	}
	for _, s := range extra {
		for _, f := range strings.Fields(s) {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

func (m *Manager) clientCtx(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, m.client)
	return gooidc.ClientContext(ctx, m.client)
}

func randomToken() (string, error) {
	b := make([]byte, randomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oidc: reading random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func validateConfig(p ProviderConfig, needClient bool) error {
	if p.Issuer == "" {
		return errors.New("oidc: issuer is required")
	}
	u, err := url.Parse(p.Issuer)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("oidc: issuer %q is not an http(s) URL", p.Issuer)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("oidc: issuer %q must not contain a query or fragment", p.Issuer)
	}
	if isTemplated(p.Issuer) {
		return multiTenantErr(p.Issuer)
	}
	if needClient && p.ClientID == "" {
		return errors.New("oidc: client ID is required")
	}
	return nil
}

func cacheKey(p ProviderConfig) string {
	return strings.TrimSuffix(p.Issuer, "/") + "\x00" + p.ClientID
}

// provider returns the cached discovery result, running discovery when it is
// missing or older than the TTL.
func (m *Manager) provider(ctx context.Context, p ProviderConfig) (cacheEntry, error) {
	key := cacheKey(p)
	m.mu.Lock()
	e, ok := m.cache[key]
	m.mu.Unlock()
	if ok && m.now().Before(e.expires) {
		return e, nil
	}
	e, err := m.discover(ctx, p)
	if err != nil {
		return cacheEntry{}, err
	}
	m.mu.Lock()
	m.cache[key] = e
	m.mu.Unlock()
	return e, nil
}

type discoveryDoc struct {
	Issuer     string   `json:"issuer"`
	AuthURL    string   `json:"authorization_endpoint"`
	TokenURL   string   `json:"token_endpoint"`
	UserInfo   string   `json:"userinfo_endpoint"`
	JWKSURL    string   `json:"jwks_uri"`
	Algorithms []string `json:"id_token_signing_alg_values_supported"`
}

// discover fetches <issuer without trailing slash>/.well-known/openid-configuration.
//
// go-oidc's NewProvider insists the discovered issuer equals the configured
// one byte for byte, but admins type issuers with or without a trailing slash
// (Entra v2 and Keycloak have none, Authentik has one). We accept a difference
// of exactly one trailing slash and then use the discovered issuer verbatim,
// so ID tokens are still checked against the exact string the provider
// publishes. Any other difference is an error naming both values.
//
// Entra's multi-tenant endpoints (common, organizations, consumers) publish a
// templated issuer "https://login.microsoftonline.com/{tenantid}/v2.0" whose
// tokens carry a per-tenant issuer. Validating those safely needs a tenant
// allow-list, which is out of scope: they are rejected with ErrMultiTenant and
// the admin must configure the tenant-specific issuer instead.
func (m *Manager) discover(ctx context.Context, p ProviderConfig) (cacheEntry, error) {
	wellKnown := strings.TrimSuffix(p.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return cacheEntry{}, fmt.Errorf("oidc: discovery request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return cacheEntry{}, fmt.Errorf("oidc: discovery at %s failed: %w", wellKnown, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDiscoveryBytes+1))
	if err != nil {
		return cacheEntry{}, fmt.Errorf("oidc: reading discovery document: %w", err)
	}
	if len(body) > maxDiscoveryBytes {
		return cacheEntry{}, errors.New("oidc: discovery document too large")
	}
	if resp.StatusCode != http.StatusOK {
		return cacheEntry{}, fmt.Errorf("oidc: discovery at %s returned %s", wellKnown, resp.Status)
	}
	var d discoveryDoc
	if err := json.Unmarshal(body, &d); err != nil {
		return cacheEntry{}, fmt.Errorf("oidc: decoding discovery document: %w", err)
	}
	if d.Issuer == "" {
		return cacheEntry{}, errors.New("oidc: discovery document has no issuer")
	}
	if isTemplated(d.Issuer) {
		return cacheEntry{}, multiTenantErr(d.Issuer)
	}
	if d.Issuer != p.Issuer && strings.TrimSuffix(d.Issuer, "/") != strings.TrimSuffix(p.Issuer, "/") {
		return cacheEntry{}, fmt.Errorf("%w: configured %q but the provider reports %q", ErrIssuerMismatch, p.Issuer, d.Issuer)
	}
	// TrimSuffix removes at most one slash, so "x//" vs "x" still mismatches.
	for name, v := range map[string]string{"authorization_endpoint": d.AuthURL, "token_endpoint": d.TokenURL, "jwks_uri": d.JWKSURL} {
		u, err := url.Parse(v)
		if v == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return cacheEntry{}, fmt.Errorf("oidc: discovery document has an invalid %s %q", name, v)
		}
	}
	var algs []string
	for _, a := range d.Algorithms {
		if supportedAlgs[a] {
			algs = append(algs, a)
		}
	}
	pc := gooidc.ProviderConfig{
		IssuerURL:   d.Issuer, // exact discovered value; used for token issuer checks
		AuthURL:     d.AuthURL,
		TokenURL:    d.TokenURL,
		UserInfoURL: d.UserInfo,
		JWKSURL:     d.JWKSURL,
		Algorithms:  algs,
	}
	prov := pc.NewProvider(gooidc.ClientContext(context.Background(), m.client))
	return cacheEntry{provider: prov, issuer: d.Issuer, expires: m.now().Add(m.ttl)}, nil
}

func isTemplated(s string) bool {
	return strings.Contains(s, "{") || strings.Contains(s, "%7B") || strings.Contains(s, "%7b")
}

func multiTenantErr(issuer string) error {
	return fmt.Errorf("%w: %q looks like an Entra multi-tenant endpoint (common/organizations/consumers); "+
		"configure the tenant-specific issuer https://login.microsoftonline.com/<tenant-id>/v2.0 instead",
		ErrMultiTenant, issuer)
}
