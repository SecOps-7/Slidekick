// Package accounts holds the rules for users: password hashing and checks,
// browser sessions, personal API tokens, the first admin, user administration
// and how single sign-on identities map to users and roles.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/SecOps-7/slidekick/internal/authz"
	"github.com/SecOps-7/slidekick/internal/deck"
	"github.com/SecOps-7/slidekick/internal/oidc"
	"github.com/SecOps-7/slidekick/internal/secret"
	"github.com/SecOps-7/slidekick/internal/store"
)

const (
	SessionTTL      = 14 * 24 * time.Hour
	APITokenPrefix  = "skp_"
	MinPassword     = 10
	settingEditAll  = "editors_edit_all"
	settingEmbed    = "embed_viewer"         // "1": viewer link pages may be framed by EmbedOrigins
	settingOrigins  = "embed_viewer_origins" // JSON list of origins
	maxEmbedOrigins = 20
	maxHashParallel = 2
)

var (
	ErrBadLogin   = errors.New("wrong username or password")
	ErrLastAdmin  = errors.New("this is the last active admin: make someone else an admin first")
	ErrNoAccount  = errors.New("no Slidekick account is linked to this sign-in, and automatic account creation is off for this provider: ask an admin")
	ErrValidation = errors.New("invalid")
)

type Service struct {
	Store *store.Store
	Box   *secret.Box
	OIDC  *oidc.Manager

	hashSem chan struct{}
	dummy   string

	mu        sync.Mutex
	setupCode string // one-time code for /setup while no user exists
	chat      map[string]chatToken
}

// chatToken is a short-lived, in-memory token that lets the AI chat act as
// one user through the MCP endpoint, for the length of one request.
type chatToken struct {
	user string
	exp  time.Time
}

const ChatTokenPrefix = "skc_"

// ChatToken mints a token acting as userID for at most ttl; revoke ends it early.
func (s *Service) ChatToken(userID string, ttl time.Duration) (string, func()) {
	tok := ChatTokenPrefix + randToken(30)
	h := Hash(tok)
	s.mu.Lock()
	if s.chat == nil {
		s.chat = map[string]chatToken{}
	}
	now := time.Now()
	for k, v := range s.chat {
		if now.After(v.exp) {
			delete(s.chat, k)
		}
	}
	s.chat[h] = chatToken{user: userID, exp: now.Add(ttl)}
	s.mu.Unlock()
	return tok, func() {
		s.mu.Lock()
		delete(s.chat, h)
		s.mu.Unlock()
	}
}

func New(st *store.Store, box *secret.Box, om *oidc.Manager) *Service {
	s := &Service{Store: st, Box: box, OIDC: om, hashSem: make(chan struct{}, maxHashParallel)}
	s.dummy, _ = s.HashPassword("not-a-real-password-just-for-timing")
	return s
}

// Policy returns the authorization policy with this instance's settings.
func (s *Service) Policy() authz.Policy {
	return authz.Policy{EditorsEditAll: func() bool { return s.Store.Setting(context.Background(), settingEditAll) == "1" }}
}

// ---- passwords (argon2id, PHC string format) ----

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword hashes with argon2id. At most two hashes run at once, so a
// burst of logins can't exhaust memory.
func (s *Service) HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	s.hashSem <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-s.hashSem
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func (s *Service) verifyPassword(pw, phc string) bool {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || m > 1<<20 || t > 10 || p == 0 {
		return false
	}
	s.hashSem <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	<-s.hashSem
	return subtle.ConstantTimeCompare(got, want) == 1
}

func CheckPassword(pw string) error {
	if len([]rune(pw)) < MinPassword {
		return fmt.Errorf("%w: password must be at least %d characters", ErrValidation, MinPassword)
	}
	if len(pw) > 512 {
		return fmt.Errorf("%w: password is too long", ErrValidation)
	}
	return nil
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{1,63}$`)

func checkUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return fmt.Errorf("%w: username must be 2-64 letters, digits or . _ @ -", ErrValidation)
	}
	return nil
}

// Login checks a username and password. Unknown users cost the same time as
// known ones, so usernames can't be probed by timing.
func (s *Service) Login(ctx context.Context, username, password string) (*store.User, error) {
	u, err := s.Store.UserByName(ctx, strings.TrimSpace(username))
	if err != nil || u.PasswordHash == "" {
		s.verifyPassword(password, s.dummy)
		return nil, ErrBadLogin
	}
	if !s.verifyPassword(password, u.PasswordHash) || u.Disabled {
		return nil, ErrBadLogin
	}
	s.Store.TouchLogin(ctx, u.ID)
	return u, nil
}

// ---- tokens and sessions ----

func randToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// NewSession starts a browser session; the cookie value is returned once and
// only its hash is stored.
func (s *Service) NewSession(ctx context.Context, userID, via string) (cookie, csrf string, err error) {
	cookie, csrf = randToken(32), randToken(24)
	return cookie, csrf, s.Store.CreateSession(ctx, Hash(cookie), userID, csrf, via, time.Now().Add(SessionTTL))
}

// SessionUser resolves a session cookie to an enabled user.
func (s *Service) SessionUser(ctx context.Context, cookie string) (*store.User, *store.Session, error) {
	if cookie == "" || len(cookie) > 200 {
		return nil, nil, store.ErrNotFound
	}
	ss, err := s.Store.GetSession(ctx, Hash(cookie))
	if err != nil {
		return nil, nil, err
	}
	u, err := s.Store.GetUser(ctx, ss.UserID)
	if err != nil || u.Disabled {
		return nil, nil, store.ErrNotFound
	}
	return u, ss, nil
}

func (s *Service) EndSession(ctx context.Context, cookie string) error {
	return s.Store.DeleteSession(ctx, Hash(cookie))
}

// CreateAPIToken makes a personal token (shown once) that acts as the user,
// with their role, for MCP and the REST API.
func (s *Service) CreateAPIToken(ctx context.Context, userID, name string) (string, *store.APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return "", nil, fmt.Errorf("%w: give the token a name (up to 80 characters)", ErrValidation)
	}
	tok := APITokenPrefix + randToken(30)
	t := &store.APIToken{ID: deck.NewID("t"), UserID: userID, Name: name}
	return tok, t, s.Store.CreateAPIToken(ctx, t, Hash(tok))
}

// APITokenUser resolves a personal token to an enabled user.
func (s *Service) APITokenUser(ctx context.Context, tok string) (*store.User, bool) {
	if len(tok) > 200 {
		return nil, false
	}
	var uid string
	switch {
	case strings.HasPrefix(tok, ChatTokenPrefix):
		s.mu.Lock()
		ct, ok := s.chat[Hash(tok)]
		s.mu.Unlock()
		if !ok || time.Now().After(ct.exp) {
			return nil, false
		}
		uid = ct.user
	case strings.HasPrefix(tok, APITokenPrefix):
		var err error
		if uid, err = s.Store.UserForAPIToken(ctx, Hash(tok)); err != nil {
			return nil, false
		}
	default:
		return nil, false
	}
	u, err := s.Store.GetUser(ctx, uid)
	if err != nil || u.Disabled {
		return nil, false
	}
	return u, true
}

// Principal turns a user into an authorization principal.
func Principal(u *store.User) authz.Principal {
	return authz.Principal{Kind: authz.KindUser, UserID: u.ID, Username: u.Username, Role: authz.Role(u.Role)}
}

// ---- first admin ----

// SetupNeeded reports whether no account exists yet.
func (s *Service) SetupNeeded(ctx context.Context) bool {
	n, err := s.Store.CountUsers(ctx)
	return err == nil && n == 0
}

// SetupCode returns (creating once) the one-time code that /setup needs. It is
// printed in the server log, so only someone who can read the log (or who
// holds SLIDEKICK_TOKEN) can claim the first admin.
func (s *Service) SetupCode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setupCode == "" {
		s.setupCode = strings.ToUpper(hex.EncodeToString([]byte(randToken(6)))[:12])
	}
	return s.setupCode
}

func (s *Service) CheckSetupCode(code, serverToken string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	if serverToken != "" && subtle.ConstantTimeCompare([]byte(code), []byte(serverToken)) == 1 {
		return true
	}
	s.mu.Lock()
	want := s.setupCode
	s.mu.Unlock()
	return want != "" && subtle.ConstantTimeCompare([]byte(strings.ToUpper(code)), []byte(want)) == 1
}

// CreateFirstAdmin creates the first account; it fails once any user exists.
func (s *Service) CreateFirstAdmin(ctx context.Context, username, password string) (*store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.SetupNeeded(ctx) {
		return nil, fmt.Errorf("%w: an admin already exists; sign in instead", ErrValidation)
	}
	u, err := s.CreateUser(ctx, NewUser{Username: username, Password: password, Role: "admin"})
	if err == nil {
		s.setupCode = ""
	}
	return u, err
}

// EnsureAdmin creates the admin or, if the username exists, makes it an
// enabled admin with this password. Used by SLIDEKICK_ADMIN_* on first run and
// by the `slidekick admin` recovery command.
func (s *Service) EnsureAdmin(ctx context.Context, username, password string) (*store.User, bool, error) {
	if err := CheckPassword(password); err != nil {
		return nil, false, err
	}
	u, err := s.Store.UserByName(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		u, err := s.CreateUser(ctx, NewUser{Username: username, Password: password, Role: "admin"})
		return u, true, err
	}
	if err != nil {
		return nil, false, err
	}
	h, err := s.HashPassword(password)
	if err != nil {
		return nil, false, err
	}
	u.Role, u.Disabled = "admin", false
	if err := s.Store.UpdateUser(ctx, u); err != nil {
		return nil, false, err
	}
	return u, false, s.Store.SetPassword(ctx, u.ID, h, "")
}

// ---- user administration ----

type NewUser struct {
	Username, Email, Name, Role, Password string
}

func (s *Service) CreateUser(ctx context.Context, n NewUser) (*store.User, error) {
	n.Username = strings.TrimSpace(n.Username)
	if err := checkUsername(n.Username); err != nil {
		return nil, err
	}
	role, err := authz.ParseRole(n.Role)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	u := &store.User{ID: deck.NewID("u"), Username: n.Username, Email: strings.TrimSpace(n.Email), Name: strings.TrimSpace(n.Name), Role: string(role)}
	if n.Password != "" {
		if err := CheckPassword(n.Password); err != nil {
			return nil, err
		}
		if u.PasswordHash, err = s.HashPassword(n.Password); err != nil {
			return nil, err
		}
	}
	if err := s.Store.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrExists) {
			return nil, fmt.Errorf("%w: username %q is taken", ErrValidation, n.Username)
		}
		return nil, err
	}
	u.HasPassword = u.PasswordHash != ""
	return u, nil
}

type UserUpdate struct {
	Username, Email, Name, Role *string
	Disabled                    *bool
}

// UpdateUser changes a user. It refuses to demote or disable the last active admin.
func (s *Service) UpdateUser(ctx context.Context, id string, up UserUpdate) (*store.User, error) {
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	wasAdmin := u.Role == "admin" && !u.Disabled
	if up.Username != nil {
		v := strings.TrimSpace(*up.Username)
		if err := checkUsername(v); err != nil {
			return nil, err
		}
		u.Username = v
	}
	if up.Email != nil {
		u.Email = strings.TrimSpace(*up.Email)
	}
	if up.Name != nil {
		u.Name = strings.TrimSpace(*up.Name)
	}
	if up.Role != nil {
		r, err := authz.ParseRole(*up.Role)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrValidation, err)
		}
		u.Role = string(r)
	}
	if up.Disabled != nil {
		u.Disabled = *up.Disabled
	}
	if wasAdmin && (u.Role != "admin" || u.Disabled) {
		if n, _ := s.Store.CountActiveAdmins(ctx); n <= 1 {
			return nil, ErrLastAdmin
		}
	}
	if err := s.Store.UpdateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrExists) {
			return nil, fmt.Errorf("%w: username %q is taken", ErrValidation, u.Username)
		}
		return nil, err
	}
	return u, nil
}

// SetPassword sets a user's password (admin reset or self-service) and ends
// their other sessions.
func (s *Service) SetPassword(ctx context.Context, id, password, keepSessionCookie string) error {
	if err := CheckPassword(password); err != nil {
		return err
	}
	h, err := s.HashPassword(password)
	if err != nil {
		return err
	}
	keep := ""
	if keepSessionCookie != "" {
		keep = Hash(keepSessionCookie)
	}
	return s.Store.SetPassword(ctx, id, h, keep)
}

// ChangeOwnPassword checks the current password first (unless the account has
// none yet, e.g. SSO-only).
func (s *Service) ChangeOwnPassword(ctx context.Context, u *store.User, current, next, cookie string) error {
	if u.PasswordHash != "" && !s.verifyPassword(current, u.PasswordHash) {
		return fmt.Errorf("%w: current password is wrong", ErrValidation)
	}
	return s.SetPassword(ctx, u.ID, next, cookie)
}

// DeleteUser removes a user; their decks pass to transferTo (an existing user
// id) or become admin-only.
func (s *Service) DeleteUser(ctx context.Context, id, transferTo string) error {
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if u.Role == "admin" && !u.Disabled {
		if n, _ := s.Store.CountActiveAdmins(ctx); n <= 1 {
			return ErrLastAdmin
		}
	}
	if transferTo != "" {
		if transferTo == id {
			return fmt.Errorf("%w: can't transfer decks to the user being deleted", ErrValidation)
		}
		if _, err := s.Store.GetUser(ctx, transferTo); err != nil {
			return fmt.Errorf("%w: transfer target not found", ErrValidation)
		}
	}
	return s.Store.DeleteUser(ctx, id, transferTo)
}

// ---- instance settings ----

type Settings struct {
	EditorsEditAll bool `json:"editorsEditAll"`
	// EmbedViewer lets other sites show viewer links in a frame; only the
	// origins in EmbedOrigins may (frame-ancestors). Off by default.
	EmbedViewer  bool     `json:"embedViewer"`
	EmbedOrigins []string `json:"embedOrigins"`
}

func (s *Service) Settings(ctx context.Context) Settings {
	v := Settings{EditorsEditAll: s.Store.Setting(ctx, settingEditAll) == "1", EmbedViewer: s.Store.Setting(ctx, settingEmbed) == "1", EmbedOrigins: []string{}}
	if raw := s.Store.Setting(ctx, settingOrigins); raw != "" {
		json.Unmarshal([]byte(raw), &v.EmbedOrigins)
	}
	return v
}

// EmbedOrigins are the origins allowed to frame viewer link pages right now
// (none while embedding is off).
func (s *Service) EmbedOrigins(ctx context.Context) []string {
	if s.Store.Setting(ctx, settingEmbed) != "1" {
		return nil
	}
	return s.Settings(ctx).EmbedOrigins
}

func (s *Service) SaveSettings(ctx context.Context, v Settings) error {
	origins := make([]string, 0, len(v.EmbedOrigins))
	seen := map[string]bool{}
	for _, raw := range v.EmbedOrigins {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		o, err := NormalizeOrigin(raw)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrValidation, err.Error())
		}
		if !seen[o] {
			seen[o] = true
			origins = append(origins, o)
		}
	}
	if len(origins) > maxEmbedOrigins {
		return fmt.Errorf("%w: at most %d sites may embed viewer links", ErrValidation, maxEmbedOrigins)
	}
	if v.EmbedViewer && len(origins) == 0 {
		return fmt.Errorf("%w: list at least one site (like https://dashboard.example:8443) that may embed viewer links", ErrValidation)
	}
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	list, _ := json.Marshal(origins)
	for k, val := range map[string]string{settingEditAll: flag(v.EditorsEditAll), settingOrigins: string(list), settingEmbed: flag(v.EmbedViewer)} {
		if err := s.Store.SetSetting(ctx, k, val); err != nil {
			return err
		}
	}
	return nil
}

var originHost = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// NormalizeOrigin checks a site origin for frame-ancestors and returns it in
// canonical form: scheme://host[:port], lower case, without a default port.
// No paths, wildcards, user info or anything that could break out of the
// header.
func NormalizeOrigin(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("%q isn't a site origin: use the scheme, host and port only, like https://dashboard.example:8443", raw)
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Is6() {
			host = "[" + ip.String() + "]"
		} else {
			host = ip.String()
		}
	} else if !originHost.MatchString(host) {
		return "", fmt.Errorf("%q isn't a site origin: the host may only use letters, digits, dots and dashes (no wildcards)", raw)
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("%q has an invalid port", raw)
		}
		if (u.Scheme == "https" && n == 443) || (u.Scheme == "http" && n == 80) {
			port = ""
		}
	}
	out := u.Scheme + "://" + host
	if port != "" {
		out += ":" + port
	}
	return out, nil
}
