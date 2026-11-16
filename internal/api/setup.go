package api

import (
	"errors"
	"net/http"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/backup"
	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/vpn"
)

func (s *Server) handleWizardStatus(w http.ResponseWriter, r *http.Request) {
	if s.wiz == nil {
		writeError(w, http.StatusNotFound, "Setup is not available.")
		return
	}
	view, err := s.wiz.Status(r.Context(), nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleWizard(w http.ResponseWriter, r *http.Request) {
	if s.wiz == nil {
		writeError(w, http.StatusNotFound, "Setup is not available.")
		return
	}
	var in setup.Request
	if !readJSON(w, r, &in) {
		return
	}
	in.Client = clientKey(r)
	if p, ok := principalFrom(r.Context()); ok {
		switch {
		case p.Username != "":
			in.Actor = p.Username
		case p.TokenName != "":
			in.Actor = p.TokenName
		}
	}
	view, grant, err := s.wiz.Apply(r.Context(), in)
	if grant != nil {
		setSessionCookie(w, r, grant.Token, grant.Expires)
	}
	if err != nil {
		// The account already exists. Return the page, with the error on it,
		// so a recovery code is not dropped and the new session cookie sticks.
		if grant != nil {
			view.Error = wizardMessage(err)
			writeJSON(w, http.StatusOK, view)
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// wizardMessage is a user-facing sentence. Unknown errors stay generic.
func wizardMessage(err error) string {
	var (
		authIn   *auth.InputError
		setupIn  *setup.InputError
		setupOp  *setup.OpError
		remoteIn *remote.InputError
		remoteOp *remote.OpError
		vpnIn    *vpn.InputError
		vpnOp    *vpn.OpError
		mailIn   *notify.InputError
		mailOp   *notify.OpError
		backIn   *backup.InputError
		backOp   *backup.OpError
	)
	switch {
	case errors.As(err, &authIn):
		return authIn.Msg
	case errors.As(err, &setupIn):
		return setupIn.Msg
	case errors.As(err, &setupOp):
		return setupOp.Msg
	case errors.As(err, &remoteIn):
		return remoteIn.Msg
	case errors.As(err, &remoteOp):
		return remoteOp.Msg
	case errors.As(err, &vpnIn):
		return vpnIn.Msg
	case errors.As(err, &vpnOp):
		return vpnOp.Msg
	case errors.As(err, &mailIn):
		return mailIn.Msg
	case errors.As(err, &mailOp):
		return mailOp.Msg
	case errors.As(err, &backIn):
		return backIn.Msg
	case errors.As(err, &backOp):
		return backOp.Msg
	case errors.Is(err, auth.ErrInvalidCredentials):
		return "Incorrect username or password."
	default:
		return "Something went wrong on the server. Check the FELDCORE log."
	}
}
