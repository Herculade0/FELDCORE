package publish

import (
	"net/http"

	"github.com/Herculade0/feldcore/internal/citizenid"
)

// CitizenAuth is the AuthHook that enforces a publication's access level.
// SetAuth(AllowAll) is the stand-in used before this is installed.
func CitizenAuth(g *citizenid.Gateway) AuthHook {
	if g == nil {
		return AllowAll{}
	}
	return citizenHook{g: g}
}

type citizenHook struct {
	g *citizenid.Gateway
}

func (h citizenHook) Allow(r *http.Request, app App) error {
	level, groups := h.g.PublishedAccess(app.Container, app.Access, app.Groups)
	decision, err := h.g.Authorize(r, citizenid.AppAccess{
		ID:       app.Container,
		Name:     app.Hostname,
		Host:     app.Hostname,
		Level:    level,
		GroupIDs: groups,
	})
	if err != nil {
		return err
	}
	if decision.Allow {
		return nil
	}
	if decision.Reason == "unauthenticated" && decision.Location != "" {
		return &DenyError{Status: http.StatusFound, Msg: "Sign in to continue.", Location: decision.Location}
	}
	return &DenyError{Status: http.StatusForbidden, Msg: "You do not have access to this app."}
}
