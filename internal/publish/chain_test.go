package publish

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

type hookFunc func(*http.Request, App) error

func (f hookFunc) Allow(r *http.Request, app App) error { return f(r, app) }

func TestMiddlewareOrder(t *testing.T) {
	var steps []string
	var seen App
	h := Handler{
		Lookup: func(*http.Request) (App, bool) {
			steps = append(steps, StepLookup)
			return App{Container: "plex", Hostname: "plex.example.com", Port: 32400, Access: AccessAdmins, Groups: []string{"family"}}, true
		},
		Protect: func(*http.Request, App) Result {
			steps = append(steps, StepProtect)
			return Result{}
		},
		Auth: hookFunc(func(_ *http.Request, app App) error {
			steps = append(steps, StepAuth)
			seen = app
			return nil
		}),
		Proxy: func(w http.ResponseWriter, _ *http.Request, _ App) {
			steps = append(steps, StepProxy)
			w.WriteHeader(http.StatusNoContent)
		},
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://plex.example.com/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Join(steps, ",") != "lookup,protect,auth,proxy" {
		t.Fatalf("order %v", steps)
	}
	if seen.Access != AccessAdmins || len(seen.Groups) != 1 || seen.Groups[0] != "family" {
		t.Fatalf("hook saw %+v", seen)
	}
}

func TestProtectionBlockSkipsAuthAndProxy(t *testing.T) {
	var steps []string
	h := Handler{
		Lookup: func(*http.Request) (App, bool) {
			steps = append(steps, StepLookup)
			return App{Hostname: "plex.example.com"}, true
		},
		Protect: func(*http.Request, App) Result {
			steps = append(steps, StepProtect)
			return Result{Block: true, Status: http.StatusTooManyRequests, Reason: "rate", Msg: "Too many requests."}
		},
		Auth: hookFunc(func(*http.Request, App) error {
			steps = append(steps, StepAuth)
			return nil
		}),
		Proxy: func(http.ResponseWriter, *http.Request, App) {
			steps = append(steps, StepProxy)
		},
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Join(steps, ",") != "lookup,protect" {
		t.Fatalf("order %v", steps)
	}
}

func TestAuthHookRunsAfterProtectionAndBeforeProxy(t *testing.T) {
	var steps []string
	h := Handler{
		Lookup: func(*http.Request) (App, bool) {
			steps = append(steps, StepLookup)
			return App{Hostname: "plex.example.com", Access: AccessSignedIn}, true
		},
		Protect: func(*http.Request, App) Result {
			steps = append(steps, StepProtect)
			return Result{}
		},
		Auth: hookFunc(func(*http.Request, App) error {
			steps = append(steps, StepAuth)
			return &DenyError{Status: http.StatusUnauthorized, Msg: "Sign in with Citizen ID."}
		}),
		Proxy: func(http.ResponseWriter, *http.Request, App) {
			steps = append(steps, StepProxy)
		},
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "Citizen ID") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Join(steps, ",") != "lookup,protect,auth" {
		t.Fatalf("order %v", steps)
	}
}

func TestUnknownHostStopsBeforeProtection(t *testing.T) {
	var steps []string
	h := Handler{
		Lookup: func(*http.Request) (App, bool) {
			steps = append(steps, StepLookup)
			return App{}, false
		},
		Protect: func(*http.Request, App) Result {
			steps = append(steps, StepProtect)
			return Result{}
		},
		Auth:  AllowAll{},
		Proxy: func(http.ResponseWriter, *http.Request, App) { steps = append(steps, StepProxy) },
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound || strings.Join(steps, ",") != "lookup" {
		t.Fatalf("status %d steps %v", rec.Code, steps)
	}
}

func TestRateLimitStopsTheChain(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	p := &Protection{
		Policy: func() store.ProtectPolicy {
			return store.ProtectPolicy{Configured: true, RatePerSec: 1, RateBurst: 1, CountryMode: "off"}
		},
		Now: func() time.Time { return now },
	}
	var steps []string
	h := Handler{
		Lookup: func(*http.Request) (App, bool) {
			steps = append(steps, StepLookup)
			return App{Hostname: "plex.example.com", Container: "plex", Access: AccessPublic}, true
		},
		Protect: func(r *http.Request, app App) Result {
			steps = append(steps, StepProtect)
			return p.Check(r, app)
		},
		Auth: hookFunc(func(*http.Request, App) error {
			steps = append(steps, StepAuth)
			return nil
		}),
		Proxy: func(w http.ResponseWriter, _ *http.Request, _ App) {
			steps = append(steps, StepProxy)
			w.WriteHeader(http.StatusNoContent)
		},
	}
	call := func() (int, string) {
		steps = nil
		req := httptest.NewRequest(http.MethodGet, "http://plex.example.com/", nil)
		req.Host = "plex.example.com"
		req.RemoteAddr = "203.0.113.10:4000"
		req.Header.Set("User-Agent", "Mozilla/5.0")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, strings.Join(steps, ",")
	}
	if code, order := call(); code != http.StatusNoContent || order != "lookup,protect,auth,proxy" {
		t.Fatalf("first request %d %s", code, order)
	}
	if code, order := call(); code != http.StatusTooManyRequests || order != "lookup,protect" {
		t.Fatalf("second request %d %s", code, order)
	}
}
