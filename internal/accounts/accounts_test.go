package accounts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SecOps-7/slidekick/internal/oidc"
	"github.com/SecOps-7/slidekick/internal/secret"
	"github.com/SecOps-7/slidekick/internal/store"
)

func newSvc(t *testing.T) *Service {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _, _ := secret.Load("", t.TempDir())
	return New(st, box, oidc.NewManager(nil))
}

func TestPasswordHashing(t *testing.T) {
	s := newSvc(t)
	h, err := s.HashPassword("a long enough password")
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("hash %q %v", h, err)
	}
	h2, _ := s.HashPassword("a long enough password")
	if h == h2 {
		t.Fatal("no salt")
	}
	if !s.verifyPassword("a long enough password", h) || s.verifyPassword("a long enough passwore", h) {
		t.Fatal("verify")
	}
	if s.verifyPassword("x", "$argon2id$v=19$m=999999999,t=3,p=2$AAAA$AAAA") {
		t.Fatal("accepted absurd parameters")
	}
	if CheckPassword("short") == nil {
		t.Fatal("short password accepted")
	}
}

func TestLoginAndSessions(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	u, err := s.CreateFirstAdmin(ctx, "boss", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFirstAdmin(ctx, "evil", "correct horse battery"); err == nil {
		t.Fatal("second first admin")
	}
	if _, err := s.Login(ctx, "BOSS", "correct horse battery"); err != nil {
		t.Fatal("usernames are case-insensitive")
	}
	if _, err := s.Login(ctx, "boss", "wrong password!!"); err != ErrBadLogin {
		t.Fatal("wrong password")
	}
	cookie, csrf, _ := s.NewSession(ctx, u.ID, "password")
	if got, ss, err := s.SessionUser(ctx, cookie); err != nil || got.ID != u.ID || ss.CSRF != csrf {
		t.Fatal("session lookup")
	}
	// The cookie isn't stored in the clear.
	if _, err := s.Store.GetSession(ctx, cookie); err == nil {
		t.Fatal("raw cookie found in the session table")
	}
	s.EndSession(ctx, cookie)
	if _, _, err := s.SessionUser(ctx, cookie); err == nil {
		t.Fatal("ended session still works")
	}
	tok, _, _ := s.CreateAPIToken(ctx, u.ID, "agent")
	if got, ok := s.APITokenUser(ctx, tok); !ok || got.ID != u.ID {
		t.Fatal("api token")
	}
	if _, ok := s.APITokenUser(ctx, tok+"x"); ok {
		t.Fatal("wrong api token accepted")
	}
}

func TestRoleRules(t *testing.T) {
	p := &store.SSOProvider{RoleRules: []store.RoleRule{
		{Claim: "group", Value: "admins", Role: "admin"},
		{Claim: "domain", Value: "example.com", Role: "editor"},
		{Claim: "email", Value: "boss@other.org", Role: "admin"},
	}}
	cases := []struct {
		id   oidc.Identity
		role string
		ok   bool
	}{
		{oidc.Identity{Groups: []string{"admins"}}, "admin", true},
		{oidc.Identity{Email: "ann@example.com", EmailVerified: true}, "editor", true},
		{oidc.Identity{Email: "ann@example.com"}, "", false}, // unverified email doesn't count
		{oidc.Identity{Email: "BOSS@other.org", EmailVerified: true}, "admin", true},
		{oidc.Identity{Email: "x@else.net", EmailVerified: true}, "", false},
	}
	for i, c := range cases {
		if role, ok := RoleFor(p, c.id); role != c.role || ok != c.ok {
			t.Errorf("case %d: %q %v", i, role, ok)
		}
	}
	p.TrustEmail = true
	if role, _ := RoleFor(p, oidc.Identity{Email: "ann@example.com"}); role != "editor" {
		t.Error("trusted provider's email")
	}
}

func TestProviderSecretsEncrypted(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	sec := "super-secret-client-value"
	p, err := s.SaveProvider(ctx, ProviderInput{ID: "kc", Name: "Keycloak", Issuer: "https://kc.lan/realms/x", ClientID: "slidekick", ClientSecret: &sec, Enabled: true})
	if err != nil || !p.HasSecret || strings.Contains(p.SecretEnc, sec) {
		t.Fatalf("save: %v %+v", err, p)
	}
	// Saving without a secret keeps the stored one.
	p2, _ := s.SaveProvider(ctx, ProviderInput{ID: "kc", Name: "Keycloak 2", Issuer: "https://kc.lan/realms/x", ClientID: "slidekick", Enabled: true})
	cfg, err := s.providerConfig(p2)
	if err != nil || cfg.ClientSecret != sec {
		t.Fatal("secret lost on update")
	}
	if _, err := s.SaveProvider(ctx, ProviderInput{ID: "Bad Id", Name: "x", Issuer: "https://x", ClientID: "c"}); err == nil {
		t.Fatal("bad slug accepted")
	}
	if _, err := s.SaveProvider(ctx, ProviderInput{ID: "x", Name: "x", Issuer: "https://x", ClientID: "c", DefaultRole: "root"}); err == nil {
		t.Fatal("bad role accepted")
	}
}

func TestChatTokens(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	u, _ := s.CreateFirstAdmin(ctx, "boss", "correct horse battery")
	tok, revoke := s.ChatToken(u.ID, time.Minute)
	if got, ok := s.APITokenUser(ctx, tok); !ok || got.ID != u.ID {
		t.Fatal("chat token")
	}
	revoke()
	if _, ok := s.APITokenUser(ctx, tok); ok {
		t.Fatal("revoked chat token still works")
	}
	old, _ := s.ChatToken(u.ID, -time.Second)
	if _, ok := s.APITokenUser(ctx, old); ok {
		t.Fatal("expired chat token works")
	}
}
