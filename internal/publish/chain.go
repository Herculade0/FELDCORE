package publish

import (
	"errors"
	"net/http"
)

// Steps of the publish proxy, in the only order they run.
const (
	StepLookup  = "lookup"
	StepProtect = "protect"
	StepAuth    = "auth"
	StepProxy   = "proxy"
)

// Handler is the publish proxy chain:
//
//	route lookup → protection → AuthHook → proxy to the app
//
// A failed lookup stops the chain. A protection block stops it before
// Citizen ID and before the app. An AuthHook error stops it before the app.
type Handler struct {
	Lookup  func(*http.Request) (App, bool)
	Protect func(*http.Request, App) Result
	Auth    AuthHook
	Proxy   func(http.ResponseWriter, *http.Request, App)
	OnBlock func(*http.Request, App, Result)
}

// ServeHTTP runs the chain.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Lookup == nil {
		http.NotFound(w, r)
		return
	}
	app, ok := h.Lookup(r)
	if !ok {
		http.Error(w, "No published app uses this name.", http.StatusNotFound)
		return
	}
	var res Result
	if h.Protect != nil {
		res = h.Protect(r, app)
	}
	if res.Block {
		if h.OnBlock != nil {
			h.OnBlock(r, app, res)
		}
		writeDenied(w, res.Status, res.Msg, http.StatusForbidden, "Blocked.")
		return
	}
	hook := h.Auth
	if hook == nil {
		hook = AllowAll{}
	}
	if err := hook.Allow(r, app); err != nil {
		status := http.StatusForbidden
		msg := "Citizen ID refused this request."
		var deny *DenyError
		if errors.As(err, &deny) {
			if deny.Status != 0 {
				status = deny.Status
			}
			if deny.Msg != "" {
				msg = deny.Msg
			}
		}
		writeDenied(w, status, msg, http.StatusForbidden, msg)
		return
	}
	if h.Proxy == nil {
		http.Error(w, "This app is not reachable.", http.StatusBadGateway)
		return
	}
	h.Proxy(w, r, app)
}

func writeDenied(w http.ResponseWriter, status int, msg string, fallback int, fallbackMsg string) {
	if status == 0 {
		status = fallback
	}
	if msg == "" {
		msg = fallbackMsg
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.Error(w, msg, status)
}
