package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AhmadRaza-Khan/semii/config"
	"github.com/AhmadRaza-Khan/semii/db"
	"github.com/AhmadRaza-Khan/semii/web"
	"github.com/golang-jwt/jwt/v5"
)

const secret32 = "0123456789abcdef0123456789abcdef"

func testConfig() Config {
	return Config{
		Strategies: []string{"jwt"}, AppURL: "https://app.example", JWTAlgorithm: "HS256",
		JWTSecret: config.Secret(secret32), JWTIssuer: "iss", JWTAudience: "aud",
		AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, SessionIdle: time.Hour, SessionAbsolute: 24 * time.Hour,
	}
}

func newJWT(t *testing.T, c Config) *JWT {
	t.Helper()
	j, err := NewJWT(c)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func sign(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func baseClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{"sub": "u1", "iss": "iss", "aud": "aud", "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Minute).Unix()}
}

func TestJWTIssueParse(t *testing.T) {
	j := newJWT(t, testConfig())
	tok, exp, err := j.Issue("user-1", []string{"admin"})
	if err != nil || time.Until(exp) > 15*time.Minute {
		t.Fatal(err)
	}
	p, err := j.Parse(tok)
	if err != nil || p.ID != "user-1" || p.Kind != KindUser || !p.HasRole("admin") || p.Claims["jti"] == "" {
		t.Fatalf("%+v %v", p, err)
	}
}

// Checklist: alg=none and alg-switching rejected; expired, not-yet-valid,
// wrong iss/aud rejected.
func TestJWTRejects(t *testing.T) {
	j := newJWT(t, testConfig())
	now := time.Now()
	key := []byte(secret32)
	mod := func(f func(jwt.MapClaims)) jwt.MapClaims { c := baseClaims(now); f(c); return c }

	none := sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, baseClaims(now))
	hs512 := sign(t, jwt.SigningMethodHS512, key, baseClaims(now))
	cases := map[string]struct {
		tok  string
		want error
	}{
		"alg none":           {none, ErrInvalidCredentials},
		"alg switch HS512":   {hs512, ErrInvalidCredentials},
		"wrong key":          {sign(t, jwt.SigningMethodHS256, []byte(strings.Repeat("x", 32)), baseClaims(now)), ErrInvalidCredentials},
		"expired":            {sign(t, jwt.SigningMethodHS256, key, mod(func(c jwt.MapClaims) { c["exp"] = now.Add(-time.Minute).Unix() })), ErrTokenExpired},
		"not yet valid":      {sign(t, jwt.SigningMethodHS256, key, mod(func(c jwt.MapClaims) { c["nbf"] = now.Add(time.Minute).Unix() })), ErrInvalidCredentials},
		"issued in future":   {sign(t, jwt.SigningMethodHS256, key, mod(func(c jwt.MapClaims) { c["iat"] = now.Add(time.Minute).Unix() })), ErrInvalidCredentials},
		"wrong iss":          {sign(t, jwt.SigningMethodHS256, key, mod(func(c jwt.MapClaims) { c["iss"] = "evil" })), ErrInvalidCredentials},
		"wrong aud":          {sign(t, jwt.SigningMethodHS256, key, mod(func(c jwt.MapClaims) { c["aud"] = "other" })), ErrInvalidCredentials},
		"no exp":             {sign(t, jwt.SigningMethodHS256, key, mod(func(c jwt.MapClaims) { delete(c, "exp") })), ErrInvalidCredentials},
		"no sub":             {sign(t, jwt.SigningMethodHS256, key, mod(func(c jwt.MapClaims) { delete(c, "sub") })), ErrInvalidCredentials},
		"garbage":            {"not.a.jwt", ErrInvalidCredentials},
		"tampered signature": {sign(t, jwt.SigningMethodHS256, key, baseClaims(now)) + "x", ErrInvalidCredentials},
	}
	for name, c := range cases {
		if _, err := j.Parse(c.tok); err != c.want {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	// Within the 30s leeway is accepted.
	ok := sign(t, jwt.SigningMethodHS256, key, mod(func(c jwt.MapClaims) { c["exp"] = now.Add(-20 * time.Second).Unix() }))
	if _, err := j.Parse(ok); err != nil {
		t.Errorf("leeway: %v", err)
	}
}

func edKeyPEM(t *testing.T) (string, ed25519.PublicKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), pub
}

func TestJWTEdDSAAndKeyConfusion(t *testing.T) {
	keyPEM, pub := edKeyPEM(t)
	c := testConfig()
	c.JWTAlgorithm, c.JWTPrivateKey = "EdDSA", config.Secret(keyPEM)
	j := newJWT(t, c)
	tok, _, err := j.Issue("u", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := j.Parse(tok); err != nil || p.ID != "u" {
		t.Fatalf("%v", err)
	}
	// Classic confusion: HS256 signed with the public key bytes.
	forged := sign(t, jwt.SigningMethodHS256, []byte(pub), baseClaims(time.Now()))
	if _, err := j.Parse(forged); err != ErrInvalidCredentials {
		t.Errorf("HS256-with-public-key accepted: %v", err)
	}
	// And an HS256 verifier rejects EdDSA tokens.
	if _, err := newJWT(t, testConfig()).Parse(tok); err != ErrInvalidCredentials {
		t.Errorf("EdDSA token accepted by HS256 verifier: %v", err)
	}

	for name, mut := range map[string]func(*Config){
		"short secret": func(c *Config) { c.JWTSecret = "short" },
		"bad alg":      func(c *Config) { c.JWTAlgorithm = "RS256" },
		"bad pem":      func(c *Config) { c.JWTAlgorithm, c.JWTPrivateKey = "EdDSA", "nope" },
	} {
		bad := testConfig()
		mut(&bad)
		if _, err := NewJWT(bad); err == nil {
			t.Errorf("%s: NewJWT accepted", name)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	if err := testConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	keyPEM, _ := edKeyPEM(t)
	ed := testConfig()
	ed.JWTAlgorithm, ed.JWTPrivateKey = "EdDSA", config.Secret(keyPEM)
	if err := ed.Validate(); err != nil {
		t.Errorf("eddsa: %v", err)
	}
	cases := map[string]func(*Config){
		"no strategies": func(c *Config) { c.Strategies = nil },
		"unknown":       func(c *Config) { c.Strategies = []string{"ldap"} },
		"apikey alone":  func(c *Config) { c.Strategies = []string{"apikey"} },
		"bad app url":   func(c *Config) { c.AppURL = "localhost:3000" },
		"short secret":  func(c *Config) { c.JWTSecret = "short" },
		"bad alg":       func(c *Config) { c.JWTAlgorithm = "none" },
		"bad ed key":    func(c *Config) { c.JWTAlgorithm = "EdDSA" },
		"no issuer":     func(c *Config) { c.JWTIssuer = "" },
		"zero ttl":      func(c *Config) { c.AccessTTL = 0 },
		"non-ed pkcs8 key": func(c *Config) {
			c.JWTAlgorithm, c.JWTPrivateKey = "EdDSA", "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"
		},
	}
	for name, mut := range cases {
		c := testConfig()
		mut(&c)
		if c.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	s := testConfig()
	s.Strategies = []string{"session", "apikey"}
	s.JWTSecret = ""
	if err := s.Validate(); err != nil || !s.Has("apikey") || s.Has("jwt") {
		t.Errorf("session+apikey without jwt secret: %v", err)
	}
	if testConfig().AppOrigin() != "https://app.example" {
		t.Error("AppOrigin")
	}
}

func handlerFor(t *testing.T, as []Authenticator, policies ...web.Policy) http.Handler {
	t.Helper()
	r := web.NewRouter(web.Options{})
	route := r.HandleFunc("GET /x", func(w http.ResponseWriter, r *http.Request) error {
		p, _ := FromContext(r.Context())
		id := ""
		if p != nil {
			id = p.ID
		}
		return web.JSON(w, 200, map[string]string{"id": id})
	})
	r.HandleFunc("POST /x", func(w http.ResponseWriter, r *http.Request) error { return web.NoContent(w) }).Public()
	if len(policies) > 0 {
		route.Require(policies...)
	} else {
		route.Public()
	}
	return web.Chain(r, web.RequestID(), Middleware(as...))
}

func get(h http.Handler, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/x", nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func problemCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct{ Code string }
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return p.Code
}

func TestMiddleware(t *testing.T) {
	calls := 0
	fixed := func(p *Principal, err error) Authenticator {
		return AuthenticatorFunc(func(*http.Request) (*Principal, error) { calls++; return p, err })
	}
	absent := fixed(nil, nil)

	// No credentials: continues without a principal, policy decides.
	h := handlerFor(t, []Authenticator{absent}, Authenticated())
	w := get(h)
	if w.Code != 401 || problemCode(t, w) != "unauthenticated" || w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("unauthenticated: %d %s %v", w.Code, w.Body, w.Header())
	}

	// First principal wins.
	calls = 0
	h = handlerFor(t, []Authenticator{absent, fixed(&Principal{ID: "a"}, nil), fixed(&Principal{ID: "b"}, nil)}, Authenticated())
	if w := get(h); w.Code != 200 || !strings.Contains(w.Body.String(), `"a"`) || calls != 2 {
		t.Errorf("first wins: %d %s calls=%d", w.Code, w.Body, calls)
	}

	// Invalid credentials: 401 immediately, no fall-through.
	calls = 0
	h = handlerFor(t, []Authenticator{fixed(nil, ErrInvalidCredentials), fixed(&Principal{ID: "b"}, nil)})
	w = get(h)
	if w.Code != 401 || problemCode(t, w) != "invalid_credentials" || calls != 1 {
		t.Errorf("invalid: %d %s calls=%d", w.Code, w.Body, calls)
	}
	if w := get(handlerFor(t, []Authenticator{fixed(nil, ErrTokenExpired)})); w.Code != 401 || problemCode(t, w) != "token_expired" {
		t.Errorf("expired: %d %s", w.Code, w.Body)
	}
	if w := get(handlerFor(t, []Authenticator{fixed(nil, errors.New("db down"))})); w.Code != 500 || strings.Contains(w.Body.String(), "db down") {
		t.Errorf("store failure: %d %s", w.Code, w.Body)
	}

	// A principal already in the context (testkit) is kept.
	calls = 0
	h = handlerFor(t, []Authenticator{fixed(nil, ErrInvalidCredentials)}, Authenticated())
	req := httptest.NewRequest("GET", "/x", nil)
	req = req.WithContext(WithPrincipal(req.Context(), &Principal{ID: "injected"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "injected") || calls != 0 {
		t.Errorf("injected: %d %s", rec.Code, rec.Body)
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Error("empty context has a principal")
	}
}

func TestPolicies(t *testing.T) {
	admin := &Principal{ID: "1", Kind: KindUser, Roles: []string{"admin"}, Scopes: []string{"read", "write"}}
	key := &Principal{ID: "2", Kind: KindAPIKey, Scopes: []string{"read"}}
	check := func(pol Policy, p *Principal) int {
		r := httptest.NewRequest("GET", "/", nil)
		if p != nil {
			r = r.WithContext(WithPrincipal(r.Context(), p))
		}
		err := pol.Authorize(r)
		var we *web.Error
		switch {
		case err == nil:
			return 200
		case errors.As(err, &we):
			return we.Status
		}
		return 500
	}
	cases := []struct {
		pol  Policy
		p    *Principal
		want int
		name string
	}{
		{Authenticated(), nil, 401, "authenticated"},
		{Authenticated(), key, 200, "authenticated"},
		{Role("admin", "staff"), admin, 200, "role:admin|staff"},
		{Role("admin"), key, 403, "role:admin"},
		{Role("admin"), nil, 401, "role:admin"},
		{Scope("read", "write"), admin, 200, "scope:read+write"},
		{Scope("read", "write"), key, 403, "scope:read+write"},
		{Scope("read"), nil, 401, "scope:read"},
		{Kind(KindAPIKey), key, 200, "kind:apikey"},
		{Kind(KindAPIKey), admin, 403, "kind:apikey"},
		{Kind(KindAPIKey), nil, 401, "kind:apikey"},
		{Any(Role("admin"), Scope("read")), key, 200, "any(role:admin,scope:read)"},
		{Any(Role("admin"), Kind(KindUser)), key, 403, "any(role:admin,kind:user)"},
		{Any(Role("admin")), nil, 401, "any(role:admin)"},
		{Any(), admin, 403, "any()"},
		{All(Authenticated(), Scope("read")), key, 200, "all(authenticated,scope:read)"},
		{All(Authenticated(), Scope("write")), key, 403, "all(authenticated,scope:write)"},
		{All(Authenticated()), nil, 401, "all(authenticated)"},
	}
	for _, c := range cases {
		if got := check(c.pol, c.p); got != c.want {
			t.Errorf("%s with %v: %d, want %d", c.pol, c.p, got, c.want)
		}
		if c.pol.String() != c.name {
			t.Errorf("name %q, want %q", c.pol.String(), c.name)
		}
	}
	// Policy names appear in Routes().
	r := web.NewRouter(web.Options{})
	r.HandleFunc("GET /a", func(http.ResponseWriter, *http.Request) error { return nil }).Require(Role("admin"))
	if got := r.Routes()[0].Policy; got != "role:admin" {
		t.Errorf("routes policy %q", got)
	}
}

type memSessions struct {
	s       map[string]Session
	touched int
	deleted int
	err     error
}

func (m *memSessions) FindSession(_ context.Context, h string) (Session, error) {
	if m.err != nil {
		return Session{}, m.err
	}
	s, ok := m.s[h]
	if !ok {
		return Session{}, db.ErrNotFound
	}
	return s, nil
}
func (m *memSessions) TouchSession(_ context.Context, h string, at time.Time) error {
	m.touched++
	s := m.s[h]
	s.LastSeenAt = at
	m.s[h] = s
	return nil
}
func (m *memSessions) DeleteSession(_ context.Context, h string) error {
	m.deleted++
	delete(m.s, h)
	return nil
}

func TestSessions(t *testing.T) {
	store := &memSessions{s: map[string]Session{}}
	now := time.Now()
	s := NewSessions(store, testConfig(), "production", "https://other.example")
	s.now = func() time.Time { return now }

	tok, cookie, err := s.New()
	if err != nil {
		t.Fatal(err)
	}
	if cookie.Name != SessionCookie || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Value != tok.Raw || tok.Hash != HashToken(tok.Raw) {
		t.Errorf("cookie %+v", cookie)
	}
	if !s.ExpiresAt().Equal(now.Add(24 * time.Hour)) {
		t.Error("ExpiresAt")
	}
	store.s[tok.Hash] = Session{UserID: "u1", Roles: []string{"admin"}, LastSeenAt: now.Add(-30 * time.Second), ExpiresAt: now.Add(time.Hour)}

	req := func(method string, hdr ...string) *http.Request {
		r := httptest.NewRequest(method, "/", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: tok.Raw})
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		return r
	}
	p, err := s.Authenticate(req("GET"))
	if err != nil || p.ID != "u1" || p.Claims[IDHashClaim] != tok.Hash || store.touched != 0 {
		t.Fatalf("%+v %v touched=%d", p, err, store.touched)
	}
	if CookieIDHash(req("GET")) != tok.Hash || CookieIDHash(httptest.NewRequest("GET", "/", nil)) != "" {
		t.Error("CookieIDHash")
	}

	// Checklist: CSRF rejected for cross-site cookie requests.
	csrf := []struct {
		hdr  []string
		want error
	}{
		{nil, ErrCSRF},
		{[]string{"Sec-Fetch-Site", "cross-site"}, ErrCSRF},
		{[]string{"Sec-Fetch-Site", "same-site"}, ErrCSRF},
		{[]string{"Origin", "https://evil.example"}, ErrCSRF},
		{[]string{"Origin", "null"}, ErrCSRF},
		{[]string{"Sec-Fetch-Site", "same-origin"}, nil},
		{[]string{"Sec-Fetch-Site", "none"}, nil},
		{[]string{"Origin", "https://app.example"}, nil},
		{[]string{"Origin", "https://other.example"}, nil},
	}
	for _, c := range csrf {
		if _, err := s.Authenticate(req("POST", c.hdr...)); err != c.want {
			t.Errorf("POST %v: %v, want %v", c.hdr, err, c.want)
		}
	}

	// last_seen_at is written at most once a minute.
	now = now.Add(45 * time.Second)
	_, _ = s.Authenticate(req("GET"))
	if store.touched != 1 {
		t.Errorf("touched %d", store.touched)
	}
	_, _ = s.Authenticate(req("GET"))
	if store.touched != 1 {
		t.Errorf("touched again within a minute: %d", store.touched)
	}

	// Idle and absolute expiry delete the session; stale sessions count as
	// absent so the request continues anonymously.
	now = now.Add(2 * time.Hour)
	if p, err := s.Authenticate(req("GET")); p != nil || err != nil || store.deleted != 1 {
		t.Errorf("idle: %v %v deleted=%d", p, err, store.deleted)
	}
	store.s[tok.Hash] = Session{UserID: "u1", LastSeenAt: now, ExpiresAt: now.Add(-time.Second)}
	if p, err := s.Authenticate(req("GET")); p != nil || err != nil || store.deleted != 2 {
		t.Errorf("absolute: %v %v", p, err)
	}
	if p, err := s.Authenticate(req("GET")); p != nil || err != nil {
		t.Errorf("unknown session: %v %v", p, err)
	}
	if p, err := s.Authenticate(httptest.NewRequest("GET", "/", nil)); p != nil || err != nil {
		t.Error("no cookie should be absent")
	}
	store.err = errors.New("db down")
	if _, err := s.Authenticate(req("GET")); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("store error: %v", err)
	}

	dev := NewSessions(store, testConfig(), "development")
	_, c2, _ := dev.New()
	rec := httptest.NewRecorder()
	dev.ClearCookie(rec)
	if c2.Secure || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Error("development cookies are not Secure; clear expires")
	}
}

type memKeys struct {
	k       map[string]APIKey
	touched int
	err     error
}

func (m *memKeys) FindAPIKey(_ context.Context, prefix string) (APIKey, error) {
	if m.err != nil {
		return APIKey{}, m.err
	}
	k, ok := m.k[prefix]
	if !ok {
		return APIKey{}, db.ErrNotFound
	}
	return k, nil
}
func (m *memKeys) TouchAPIKey(_ context.Context, id string, at time.Time) error {
	m.touched++
	for p, k := range m.k {
		if k.ID == id {
			k.LastUsedAt = &at
			m.k[p] = k
		}
	}
	return nil
}

func TestAPIKeys(t *testing.T) {
	key, prefix, hash, err := NewAPIKey("production")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "sk_live_"+prefix+"_") || len(prefix) != 8 {
		t.Errorf("format %s", key)
	}
	p2, secret, ok := ParseAPIKey(key)
	if !ok || p2 != prefix || HashToken(secret) != hash || len(secret) < 40 {
		t.Errorf("parse %v %s", ok, secret)
	}
	if k, _, _, _ := NewAPIKey("test"); !strings.HasPrefix(k, "sk_test_") {
		t.Error(k)
	}
	if k, _, _, _ := NewAPIKey("development"); !strings.HasPrefix(k, "sk_dev_") {
		t.Error(k)
	}
	for _, bad := range []string{"", "sk_live", "pk_live_abcdefgh_x", "sk_live_short_x", "sk_live_abcdefgh_", "sk_live_abcdefgh_x_y"} {
		if _, _, ok := ParseAPIKey(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}

	now := time.Now()
	store := &memKeys{k: map[string]APIKey{prefix: {ID: "k1", OwnerID: "u1", Scopes: []string{"read"}, SecretHash: hash}}}
	a := NewAPIKeys(store)
	a.now = func() time.Time { return now }
	auth := func(hdr ...string) (*Principal, error) {
		r := httptest.NewRequest("GET", "/", nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		return a.Authenticate(r)
	}
	p, err := auth("X-API-Key", key)
	if err != nil || p.ID != "u1" || p.Kind != KindAPIKey || !p.HasScopes("read") || p.Claims[APIKeyIDClaim] != "k1" || store.touched != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err := auth("Authorization", "Bearer "+key); err != nil || p == nil || store.touched != 1 {
		t.Errorf("bearer: %v touched=%d", err, store.touched)
	}
	now = now.Add(6 * time.Minute)
	_, _ = auth("X-API-Key", key)
	if store.touched != 2 {
		t.Errorf("touch after 5m: %d", store.touched)
	}
	if p, err := auth(); p != nil || err != nil {
		t.Error("absent key")
	}
	if p, err := auth("Authorization", "Bearer some.jwt.token"); p != nil || err != nil {
		t.Error("JWT bearer must be left to the JWT authenticator")
	}

	// Checklist: wrong, revoked and expired keys rejected.
	wrong := "sk_live_" + prefix + "_" + strings.Repeat("A", 43)
	unknown := "sk_live_ZZZZZZZZ_" + secret
	for name, k := range map[string]string{"wrong secret": wrong, "unknown prefix": unknown, "malformed": "sk_nope"} {
		if _, err := auth("X-API-Key", k); err != ErrInvalidCredentials {
			t.Errorf("%s: %v", name, err)
		}
	}
	k := store.k[prefix]
	past := now.Add(-time.Second)
	k.RevokedAt = &past
	store.k[prefix] = k
	if _, err := auth("X-API-Key", key); err != ErrInvalidCredentials {
		t.Errorf("revoked: %v", err)
	}
	k.RevokedAt, k.ExpiresAt = nil, &past
	store.k[prefix] = k
	if _, err := auth("X-API-Key", key); err != ErrInvalidCredentials {
		t.Errorf("expired: %v", err)
	}
	store.err = errors.New("db down")
	if _, err := auth("X-API-Key", key); err == nil || err == ErrInvalidCredentials {
		t.Errorf("store error: %v", err)
	}

	// The JWT authenticator ignores sk_ bearer tokens.
	j := newJWT(t, testConfig())
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+key)
	if p, err := j.Authenticate(r); p != nil || err != nil {
		t.Error("JWT authenticator claimed an API key")
	}
	r.Header.Set("Authorization", "bearer garbage")
	if _, err := j.Authenticate(r); err != ErrInvalidCredentials {
		t.Errorf("bad jwt: %v", err)
	}
}

func TestTokens(t *testing.T) {
	a, _ := NewToken()
	b, _ := NewToken()
	if a.Raw == b.Raw || len(a.Raw) != 43 || len(a.Hash) != 64 || a.Hash != HashToken(a.Raw) {
		t.Errorf("%+v", a)
	}
	if !EqualHash(a.Hash, a.Hash) || EqualHash(a.Hash, b.Hash) || EqualHash(a.Hash, a.Hash[:10]) {
		t.Error("EqualHash")
	}
}

func TestLimiterAndBackoff(t *testing.T) {
	now := time.Now()
	l := NewLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	for i := range 3 {
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatalf("hit %d refused", i)
		}
		now = now.Add(time.Second)
	}
	ok, retry := l.Allow("ip")
	if ok || retry <= 0 || retry > time.Minute {
		t.Errorf("4th: %v %v", ok, retry)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Error("keys are independent")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.Allow("ip"); !ok {
		t.Error("window did not slide")
	}
	for i := range 10_001 {
		l.Allow(string(rune(i + 1000)))
	}
	now = now.Add(2 * time.Minute)
	l.Allow("trigger-prune")
	if len(l.hits) > 2 {
		t.Errorf("prune left %d keys", len(l.hits))
	}
	want := map[int]time.Duration{0: 0, 4: 0, 5: time.Minute, 6: 5 * time.Minute, 7: 15 * time.Minute, 8: time.Hour, 50: time.Hour}
	for n, d := range want {
		if LockoutBackoff(n) != d {
			t.Errorf("backoff(%d) = %v", n, LockoutBackoff(n))
		}
	}
}
