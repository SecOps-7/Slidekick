package accounts

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/oidc"
	"github.com/SecOps-7/slidekick/internal/store"
)

// ProviderInput is what an admin submits for a sign-in provider. A nil
// ClientSecret keeps the stored one; the secret is never sent back.
type ProviderInput struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Issuer       string           `json:"issuer"`
	ClientID     string           `json:"clientId"`
	ClientSecret *string          `json:"clientSecret,omitempty"`
	Scopes       string           `json:"scopes"`
	GroupsClaim  string           `json:"groupsClaim"`
	TrustEmail   bool             `json:"trustEmail"`
	AutoCreate   bool             `json:"autoCreate"`
	DefaultRole  string           `json:"defaultRole"`
	RoleRules    []store.RoleRule `json:"roleRules"`
	Enabled      bool             `json:"enabled"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func secretPurpose(id string) string { return "sso:" + id }

// SaveProvider validates and stores a provider, encrypting its client secret.
func (s *Service) SaveProvider(ctx context.Context, in ProviderInput) (*store.SSOProvider, error) {
	in.ID = strings.ToLower(strings.TrimSpace(in.ID))
	if !slugRe.MatchString(in.ID) {
		return nil, fmt.Errorf("%w: id must be a short slug (a-z, 0-9, -), e.g. entra", ErrValidation)
	}
	in.Name, in.Issuer, in.ClientID = strings.TrimSpace(in.Name), strings.TrimSpace(in.Issuer), strings.TrimSpace(in.ClientID)
	if in.Name == "" || in.Issuer == "" || in.ClientID == "" {
		return nil, fmt.Errorf("%w: name, issuer and client ID are required", ErrValidation)
	}
	if !strings.HasPrefix(in.Issuer, "https://") && !strings.HasPrefix(in.Issuer, "http://") {
		return nil, fmt.Errorf("%w: issuer must be an http(s) URL", ErrValidation)
	}
	if in.DefaultRole == "" {
		in.DefaultRole = "viewer"
	}
	if _, err := authz.ParseRole(in.DefaultRole); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	for i, r := range in.RoleRules {
		if _, err := authz.ParseRole(r.Role); err != nil {
			return nil, fmt.Errorf("%w: rule %d: %v", ErrValidation, i+1, err)
		}
		switch r.Claim {
		case "email", "domain", "group":
		default:
			return nil, fmt.Errorf("%w: rule %d: claim must be email, domain or group", ErrValidation, i+1)
		}
		if strings.TrimSpace(r.Value) == "" {
			return nil, fmt.Errorf("%w: rule %d needs a value", ErrValidation, i+1)
		}
	}
	p, err := s.Store.GetSSOProvider(ctx, in.ID)
	if errors.Is(err, store.ErrNotFound) {
		p, err = &store.SSOProvider{ID: in.ID}, nil
	}
	if err != nil {
		return nil, err
	}
	if in.ClientSecret != nil {
		if *in.ClientSecret == "" {
			p.SecretEnc = ""
		} else if p.SecretEnc, err = s.Box.Seal(*in.ClientSecret, secretPurpose(in.ID)); err != nil {
			return nil, err
		}
	}
	if in.GroupsClaim == "" {
		in.GroupsClaim = oidc.DefaultGroupsClaim
	}
	p.Name, p.Issuer, p.ClientID, p.Scopes, p.GroupsClaim = in.Name, in.Issuer, in.ClientID, strings.TrimSpace(in.Scopes), in.GroupsClaim
	p.TrustEmail, p.AutoCreate, p.DefaultRole, p.RoleRules, p.Enabled = in.TrustEmail, in.AutoCreate, in.DefaultRole, in.RoleRules, in.Enabled
	if p.RoleRules == nil {
		p.RoleRules = []store.RoleRule{}
	}
	if err := s.Store.SaveSSOProvider(ctx, p); err != nil {
		return nil, err
	}
	p.HasSecret = p.SecretEnc != ""
	return p, nil
}

func (s *Service) providerConfig(p *store.SSOProvider) (oidc.ProviderConfig, error) {
	sec, err := s.Box.Open(p.SecretEnc, secretPurpose(p.ID))
	if err != nil {
		return oidc.ProviderConfig{}, err
	}
	return oidc.ProviderConfig{ID: p.ID, Name: p.Name, Issuer: p.Issuer, ClientID: p.ClientID, ClientSecret: sec,
		Scopes: strings.Fields(p.Scopes), GroupsClaim: p.GroupsClaim}, nil
}

// CheckProvider runs discovery for the settings page's test button.
func (s *Service) CheckProvider(ctx context.Context, id string) (string, error) {
	p, err := s.Store.GetSSOProvider(ctx, id)
	if err != nil {
		return "", err
	}
	cfg, err := s.providerConfig(p)
	if err != nil {
		return "", err
	}
	return s.OIDC.Check(ctx, cfg)
}

// BeginSSO starts a sign-in and returns the provider URL to redirect to and
// the state to bind to the browser in a cookie.
func (s *Service) BeginSSO(ctx context.Context, providerID, redirectURL, next string) (string, string, error) {
	p, err := s.Store.GetSSOProvider(ctx, providerID)
	if err != nil || !p.Enabled {
		return "", "", store.ErrNotFound
	}
	cfg, err := s.providerConfig(p)
	if err != nil {
		return "", "", err
	}
	authURL, f, err := s.OIDC.Begin(ctx, cfg, redirectURL)
	if err != nil {
		return "", "", err
	}
	s.Store.PruneExpired(ctx)
	err = s.Store.SaveSSOFlow(ctx, store.SSOFlow{State: f.State, ProviderID: f.ProviderID, Nonce: f.Nonce, Verifier: f.Verifier, Next: next, Created: f.Created})
	return authURL, f.State, err
}

// FinishSSO validates the callback (the state in the query must equal the
// browser's cookie and a stored, unused flow for this provider) and returns
// the signed-in user and where to go next.
func (s *Service) FinishSSO(ctx context.Context, providerID, redirectURL, cookieState string, query map[string][]string) (*store.User, string, error) {
	state, code, err := oidc.ParseCallback(query)
	if err != nil {
		return nil, "", err
	}
	if cookieState == "" || cookieState != state {
		return nil, "", oidc.ErrStateMismatch
	}
	sf, err := s.Store.TakeSSOFlow(ctx, state)
	if err != nil || sf.ProviderID != providerID {
		return nil, "", oidc.ErrStateMismatch
	}
	p, err := s.Store.GetSSOProvider(ctx, providerID)
	if err != nil || !p.Enabled {
		return nil, "", store.ErrNotFound
	}
	cfg, err := s.providerConfig(p)
	if err != nil {
		return nil, "", err
	}
	id, err := s.OIDC.Finish(ctx, cfg, redirectURL, oidc.Flow{State: sf.State, Nonce: sf.Nonce, Verifier: sf.Verifier, ProviderID: sf.ProviderID, Created: sf.Created}, state, code)
	if err != nil {
		return nil, "", err
	}
	u, err := s.userForIdentity(ctx, p, id)
	if err != nil {
		return nil, "", err
	}
	s.Store.TouchLogin(ctx, u.ID)
	return u, sf.Next, nil
}

// RoleFor applies a provider's rules to an identity: the first matching rule
// wins; ok is false when none matches. Email and domain rules only count when
// the provider vouches for the email.
func RoleFor(p *store.SSOProvider, id oidc.Identity) (string, bool) {
	email := strings.ToLower(id.Email)
	domain := ""
	if i := strings.LastIndex(email, "@"); i >= 0 {
		domain = email[i+1:]
	}
	trusted := id.EmailVerified || p.TrustEmail
	for _, r := range p.RoleRules {
		v := strings.TrimSpace(r.Value)
		switch r.Claim {
		case "email":
			if trusted && email != "" && strings.EqualFold(email, v) {
				return r.Role, true
			}
		case "domain":
			if trusted && domain != "" && strings.EqualFold(domain, strings.TrimPrefix(v, "@")) {
				return r.Role, true
			}
		case "group":
			for _, g := range id.Groups {
				if g == v {
					return r.Role, true
				}
			}
		}
	}
	return "", false
}

// userForIdentity finds or creates the account for a verified identity:
//  1. an account already linked to this provider and subject;
//  2. else an account with the same email, but only if the provider vouches
//     for the email (email_verified, or the admin marked the provider trusted),
//     so nobody can take over an account with an unverified address;
//  3. else a new account, if the provider allows automatic creation.
//
// When a role rule matches, the account's role follows it on every sign-in.
func (s *Service) userForIdentity(ctx context.Context, p *store.SSOProvider, id oidc.Identity) (*store.User, error) {
	role, matched := RoleFor(p, id)
	trusted := id.EmailVerified || p.TrustEmail
	var u *store.User
	if uid, err := s.Store.UserForIdentity(ctx, p.ID, id.Subject); err == nil {
		u, _ = s.Store.GetUser(ctx, uid)
	}
	if u == nil && trusted && id.Email != "" {
		if us, _ := s.Store.UsersByEmail(ctx, id.Email); len(us) == 1 {
			u = us[0]
		}
	}
	if u == nil {
		if !p.AutoCreate {
			return nil, ErrNoAccount
		}
		if !matched {
			role = p.DefaultRole
		}
		email := ""
		if trusted {
			email = id.Email
		}
		nu, err := s.CreateUser(ctx, NewUser{Username: s.freeUsername(ctx, id), Email: email, Name: id.Name, Role: role})
		if err != nil {
			return nil, err
		}
		u = nu
	} else if matched && u.Role != role {
		if nu, err := s.UpdateUser(ctx, u.ID, UserUpdate{Role: &role}); err == nil {
			u = nu
		}
	}
	if u.Disabled {
		return nil, ErrBadLogin
	}
	if err := s.Store.LinkIdentity(ctx, p.ID, id.Subject, u.ID); err != nil {
		return nil, err
	}
	return u, nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._@-]+`)

func (s *Service) freeUsername(ctx context.Context, id oidc.Identity) string {
	base := id.Email
	if i := strings.Index(base, "@"); i > 0 {
		base = base[:i]
	}
	if base == "" {
		base = id.Name
	}
	base = strings.Trim(unsafeName.ReplaceAllString(base, "."), ".-_@")
	if len(base) < 2 {
		base = "user"
	}
	if len(base) > 48 {
		base = base[:48]
	}
	name := base
	for i := 2; i < 1000; i++ {
		if _, err := s.Store.UserByName(ctx, name); errors.Is(err, store.ErrNotFound) {
			return name
		}
		name = fmt.Sprintf("%s%d", base, i)
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano()%1e6)
}
