// Package web is semii's HTTP layer: a thin router over http.ServeMux with
// groups, error-returning handlers and per-route authorization policies,
// plus RFC 9457 problem responses, JSON helpers, pagination and the
// built-in middleware.
//
// Routes are deny-by-default: every route must be marked Public or carry at
// least one Policy, either directly or through its group. Verify reports
// routes that have neither and is called at boot by generated code.
package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/semii/semii/db"
	"github.com/semii/semii/validate"
)

// HandlerFunc is an HTTP handler that reports failure by returning an error.
// The router renders the error as a problem response unless the handler
// has already written a response.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Policy authorizes a request. A non-nil error denies it and is rendered
// through the router's error handling, so returning ErrUnauthenticated
// yields 401 and ErrForbidden yields 403.
//
// Policies that implement fmt.Stringer are listed by that name in Routes.
// Package auth provides the standard policies.
type Policy interface {
	Authorize(r *http.Request) error
}

// PolicyFunc adapts a function to Policy.
type PolicyFunc func(r *http.Request) error

// Authorize calls f(r).
func (f PolicyFunc) Authorize(r *http.Request) error { return f(r) }

// Options configures a Router.
type Options struct {
	// Logger receives handler errors and boot warnings. Default: discard.
	Logger *slog.Logger
	// Validator is used by Decode and DecodeQuery. Default: validate.Default.
	Validator *validate.Validator
	// Env is the application environment. "development" and "test" include
	// error details in 500 responses and enable boot warnings; any other
	// value is treated as production.
	Env string
	// ErrorHandler, if set, replaces the default rendering of handler and
	// policy errors. It is not called when the handler already wrote.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)
}

// core is the state shared by a router and all of its groups.
type core struct {
	mux       *http.ServeMux
	routes    []*Route
	log       *slog.Logger
	validator *validate.Validator
	dev       bool
	render    errorRenderer
	custom    func(http.ResponseWriter, *http.Request, error)

	// policyGen changes whenever any policy is set, invalidating the
	// per-route cache of resolved policies.
	policyGen uint64

	// patterns holds every registered pattern, for 405 detection.
	patterns map[string]bool
	methods  []string     // methods used by routes, for 405 detection
	root     http.Handler // user handler for method-less "/", if any

	// Per-request writers are pooled so routing stays allocation-light.
	writers sync.Pool // *responseWriter
}

// Router registers routes on an http.ServeMux. Groups created with Group
// share the same mux. Configure a router fully before serving requests;
// it is not safe to register routes concurrently with ServeHTTP.
type Router struct {
	c      *core
	parent *Router
	prefix string
	mw     []Middleware
	policy *policySpec // group default; nil inherits from parent
}

type policySpec struct {
	public   bool
	policies []Policy
}

// NewRouter returns a root Router with the default error mappings:
//
//	db.ErrNotFound            404 not_found
//	db.ErrConflict            409 conflict
//	ErrUnauthenticated        401 unauthenticated
//	ErrForbidden              403 forbidden
//	ErrInvalidCredentials     401 invalid_credentials
func NewRouter(opts Options) *Router {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	v := opts.Validator
	if v == nil {
		v = validate.Default
	}
	dev := opts.Env == "development" || opts.Env == "test"
	c := &core{
		mux: http.NewServeMux(), log: log, validator: v, dev: dev,
		render: errorRenderer{log: log, dev: dev}, custom: opts.ErrorHandler,
	}
	c.policyGen = 1
	c.patterns = map[string]bool{}
	c.methods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	c.mux.Handle(fallbackPattern, http.HandlerFunc(c.fallback))
	c.writers.New = func() any { return new(responseWriter) }
	r := &Router{c: c}
	r.MapError(db.ErrNotFound, http.StatusNotFound, "not_found")
	r.MapError(db.ErrConflict, http.StatusConflict, "conflict")
	r.MapError(ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated")
	r.MapError(ErrForbidden, http.StatusForbidden, "forbidden")
	r.MapError(ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials")
	return r
}

// MapError renders errors matching target (via errors.Is) with the given
// status and code. Mappings are checked in registration order and apply to
// the whole router, including groups. The detail is target's message, never
// the wrapped error's, so internal context is not leaked.
func (r *Router) MapError(target error, status int, code string) {
	r.c.render.mappings = append(r.c.render.mappings, errorMapping{target, status, code})
}

// Group returns a sub-router whose routes are prefixed with prefix and
// wrapped by mw (mw[0] outermost), after the parent's middleware. The group
// inherits the parent's default policy until Public or Require is called.
func (r *Router) Group(prefix string, mw ...Middleware) *Router {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" && !strings.HasPrefix(prefix, "/") {
		panic(fmt.Sprintf("web: group prefix %q must start with /", prefix))
	}
	all := make([]Middleware, 0, len(r.mw)+len(mw))
	all = append(append(all, r.mw...), mw...)
	return &Router{c: r.c, parent: r, prefix: r.prefix + prefix, mw: all}
}

// Public makes routes of this router public unless a route overrides it.
func (r *Router) Public() *Router {
	r.policy = &policySpec{public: true}
	r.c.policyGen++
	return r
}

// Require makes routes of this router require all of the given policies
// unless a route overrides it.
func (r *Router) Require(p ...Policy) *Router {
	r.policy = newPolicySpec(p)
	r.c.policyGen++
	return r
}

func newPolicySpec(p []Policy) *policySpec {
	if len(p) == 0 {
		panic("web: Require needs at least one policy; use Public for open routes")
	}
	for _, pol := range p {
		if pol == nil {
			panic("web: Require called with a nil policy")
		}
	}
	return &policySpec{policies: p}
}

// Handle registers h for pattern, using http.ServeMux syntax
// ("GET /orders/{id}"). The group prefix is spliced in after the method
// and host. As a convention, the path "/" inside a prefixed group registers
// the group path itself ("GET /" in group "/orders" is "GET /orders"); use
// "/{$}" or a trailing-slash path for the other ServeMux meanings.
func (r *Router) Handle(pattern string, h http.Handler) *Route {
	if h == nil {
		panic("web: nil handler for " + pattern)
	}
	return r.handle(pattern, endpoint{h: h}, handlerName(h), false)
}

// HandleFunc registers an error-returning handler for pattern.
func (r *Router) HandleFunc(pattern string, h HandlerFunc) *Route {
	if h == nil {
		panic("web: nil handler for " + pattern)
	}
	return r.handle(pattern, endpoint{fn: h}, funcName(h), false)
}

// Mount serves h for every path under prefix, with prefix stripped from
// the request path. Requests for prefix itself are redirected to prefix+"/".
func (r *Router) Mount(prefix string, h http.Handler) *Route {
	prefix = strings.TrimSuffix(prefix, "/")
	full := r.prefix + prefix
	if h == nil {
		panic("web: nil handler for mount " + prefix)
	}
	return r.handle(prefix+"/", endpoint{h: http.StripPrefix(full, h)}, handlerName(h), true)
}

func (r *Router) handle(pattern string, ep endpoint, name string, mounted bool) *Route {
	method, host, path := splitPattern(pattern)
	if !strings.HasPrefix(path, "/") {
		panic(fmt.Sprintf("web: pattern %q: path must start with /", pattern))
	}
	if r.prefix != "" {
		if path == "/" && !mounted {
			path = r.prefix
		} else {
			path = r.prefix + path
		}
	}
	full := host + path
	if method != "" {
		full = method + " " + full
	}
	rt := &Route{
		group: r, method: method, path: host + path, pattern: full,
		handler: name, mounted: mounted,
	}

	ep.rt, ep.c = rt, r.c
	var h http.Handler = ep
	if len(r.mw) > 0 {
		// Record the pattern before group middleware runs, so their
		// responses are logged with the route too.
		ep.setPattern = false
		h = Chain(ep, r.mw...)
		h = routeEntry{rt: rt, next: h}
	} else {
		ep.setPattern = true
		h = ep
	}
	if full == fallbackPattern {
		r.c.root = h // takes over unmatched requests from the fallback
	} else {
		r.c.mux.Handle(full, h)
	}
	r.c.routes = append(r.c.routes, rt)
	r.c.patterns[full] = true
	if method != "" && !slices.Contains(r.c.methods, method) {
		r.c.methods = append(r.c.methods, method)
	}
	return rt
}

func (c *core) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if c.custom != nil {
		c.custom(w, r, err)
		return
	}
	if e := c.render.resolve(r, err); e != nil {
		WriteProblem(w, r, e)
	}
}

// ServeHTTP dispatches the request. Unmatched paths and methods are
// answered with 404 and 405 problem responses.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	c := r.c
	if c.validator != validate.Default {
		var s *reqState
		s, req = withState(req)
		s.validator = c.validator
	}
	c.mux.ServeHTTP(w, req)
}

// fallbackPattern is registered by NewRouter so requests that match no route
// reach fallback instead of ServeMux's plain-text 404/405.
const fallbackPattern = "/"

// fallback serves requests no route matched. A method-less "/" registered
// by the application (for example Mount("/", h)) owns them; otherwise it
// answers 405 with an Allow header when the path matches a route under
// another method, and 404 otherwise. It runs only on the error path.
func (c *core) fallback(w http.ResponseWriter, r *http.Request) {
	if c.root != nil {
		c.root.ServeHTTP(w, r)
		return
	}
	var allow []string
	probe := *r
	for _, m := range c.methods {
		probe.Method = m
		if _, pat := c.mux.Handler(&probe); c.patterns[pat] {
			allow = append(allow, m)
		}
	}
	if len(allow) == 0 {
		WriteProblem(w, r, NotFound("no route matches "+r.URL.Path))
		return
	}
	w.Header().Set("Allow", strings.Join(allow, ", "))
	WriteProblem(w, r, NewError(http.StatusMethodNotAllowed, "method_not_allowed",
		"method "+r.Method+" is not allowed for "+r.URL.Path))
}

// routeEntry precedes group middleware and records the matched pattern.
type routeEntry struct {
	rt   *Route
	next http.Handler
}

func (e routeEntry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s := stateFrom(r.Context()); s != nil {
		s.pattern = e.rt.pattern
	}
	e.next.ServeHTTP(w, r)
}

// endpoint is the innermost handler of every route: it enforces the
// route's policy and runs either an error-returning fn or a plain h.
type endpoint struct {
	rt         *Route
	c          *core
	fn         HandlerFunc
	h          http.Handler
	setPattern bool // no routeEntry in front of this endpoint
}

func (e endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if e.setPattern {
		if s := stateFrom(r.Context()); s != nil {
			s.pattern = e.rt.pattern
		}
	}
	spec := e.rt.resolvedPolicy(e.c.policyGen)
	if spec == nil {
		// Unreachable when Verify passed at boot; fail closed regardless.
		e.c.writeError(w, r, &Error{
			Status: http.StatusForbidden, Code: "forbidden", Detail: "route has no policy",
			Err: errors.New("web: route " + e.rt.pattern + " has no policy"),
		})
		return
	}
	for _, pol := range spec.policies {
		if err := pol.Authorize(r); err != nil {
			e.c.writeError(w, r, err)
			return
		}
	}
	if e.fn == nil {
		e.h.ServeHTTP(w, r)
		return
	}

	// Track whether fn wrote. semii's own writers (Logger, Recover, Gzip)
	// already know; otherwise borrow a tracking writer from the pool. On
	// panic a borrowed writer is simply dropped.
	if wt, ok := w.(writeTracker); ok {
		e.finish(w, r, e.fn(w, r), wt)
		return
	}
	rw := e.c.writers.Get().(*responseWriter)
	*rw = responseWriter{ResponseWriter: w}
	e.finish(rw, r, e.fn(rw, r), rw)
	*rw = responseWriter{}
	e.c.writers.Put(rw)
}

func (e endpoint) finish(w http.ResponseWriter, r *http.Request, err error, wt writeTracker) {
	if err == nil {
		return
	}
	if wt.wroteHeader() {
		e.c.render.logError(r, err)
		return
	}
	e.c.writeError(w, r, err)
}

// Route is a registered route. Its methods configure the route and return
// it for chaining.
type Route struct {
	group   *Router
	method  string
	path    string
	pattern string
	name    string
	handler string
	mounted bool
	policy  *policySpec

	cacheGen uint64 // policyGen when cached was resolved
	cached   *policySpec
}

// Public marks the route as public, overriding its group.
func (rt *Route) Public() *Route {
	rt.policy = &policySpec{public: true}
	rt.group.c.policyGen++
	return rt
}

// Require requires all of the given policies, overriding its group.
func (rt *Route) Require(p ...Policy) *Route {
	rt.policy = newPolicySpec(p)
	rt.group.c.policyGen++
	return rt
}

// Name sets a human-readable route name shown by Routes.
func (rt *Route) Name(n string) *Route {
	rt.name = n
	return rt
}

// resolvedPolicy is effectivePolicy cached until a policy changes.
func (rt *Route) resolvedPolicy(gen uint64) *policySpec {
	if rt.cacheGen != gen {
		rt.cached, rt.cacheGen = rt.effectivePolicy(), gen
	}
	return rt.cached
}

// effectivePolicy resolves route policy > nearest group policy > none.
func (rt *Route) effectivePolicy() *policySpec {
	if rt.policy != nil {
		return rt.policy
	}
	for g := rt.group; g != nil; g = g.parent {
		if g.policy != nil {
			return g.policy
		}
	}
	return nil
}

// RouteInfo describes a registered route.
type RouteInfo struct {
	Method  string `json:"method"` // "" for method-less patterns
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
	Policy  string `json:"policy"` // "public", policy names joined by ",", or "" if none
	Handler string `json:"handler"`
	Name    string `json:"name,omitempty"`
}

// Routes lists all routes in registration order.
func (r *Router) Routes() []RouteInfo {
	out := make([]RouteInfo, 0, len(r.c.routes))
	for _, rt := range r.c.routes {
		out = append(out, RouteInfo{
			Method: rt.method, Path: rt.path, Pattern: rt.pattern,
			Policy: policyName(rt.effectivePolicy()), Handler: rt.handler, Name: rt.name,
		})
	}
	return out
}

// Verify returns an error listing every route without a policy. In
// development it also logs a warning for method-less patterns.
func (r *Router) Verify() error {
	var missing []string
	for _, rt := range r.c.routes {
		if rt.effectivePolicy() == nil {
			missing = append(missing, rt.pattern)
		}
		if r.c.dev && rt.method == "" && !rt.mounted {
			r.c.log.Warn("route matches every method; prefer \"METHOD /path\"", "pattern", rt.pattern)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("web: %d route(s) have no policy (mark Public() or Require(...)): %s",
			len(missing), strings.Join(missing, ", "))
	}
	return nil
}

func policyName(spec *policySpec) string {
	switch {
	case spec == nil:
		return ""
	case spec.public:
		return "public"
	}
	names := make([]string, len(spec.policies))
	for i, p := range spec.policies {
		if s, ok := p.(fmt.Stringer); ok {
			names[i] = s.String()
		} else {
			names[i] = "custom"
		}
	}
	return strings.Join(names, ",")
}

// splitPattern splits "[METHOD ][HOST]/PATH".
func splitPattern(p string) (method, host, path string) {
	p = strings.TrimSpace(p)
	if i := strings.IndexAny(p, " \t"); i >= 0 {
		method, p = p[:i], strings.TrimLeft(p[i+1:], " \t")
	}
	if i := strings.IndexByte(p, '/'); i > 0 {
		host, p = p[:i], p[i:]
	}
	return method, host, p
}

func handlerName(h http.Handler) string {
	if f, ok := h.(http.HandlerFunc); ok {
		return funcName(f)
	}
	return fmt.Sprintf("%T", h)
}

// funcName returns "pkg.(*T).Method" for a function value. This is the only
// use of reflection in web outside query binding, and runs at registration.
func funcName(f any) string {
	fn := runtime.FuncForPC(reflect.ValueOf(f).Pointer())
	if fn == nil {
		return "?"
	}
	name := fn.Name()
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSuffix(name, "-fm")
}
