package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/auth"
)

func (s *Server) handleTokensList(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusNotFound, "Agent access is not available.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": s.auth.Tokens()})
}

type createdToken struct {
	auth.TokenInfo
	Token string `json:"token"`
}

func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays int      `json:"expiresInDays"`
	}
	if s.auth == nil {
		writeError(w, http.StatusNotFound, "Agent access is not available.")
		return
	}
	if !readJSON(w, r, &body) {
		return
	}
	secret, info, err := s.auth.CreateToken(body.Name, body.Scopes, body.ExpiresInDays)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, createdToken{TokenInfo: info, Token: secret})
}

func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusNotFound, "Agent access is not available.")
		return
	}
	if err := s.auth.RevokeToken(chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, auth.ErrTokenNotFound) {
			writeError(w, http.StatusNotFound, "That token no longer exists.")
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) handleTokensRevokeAll(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusNotFound, "Agent access is not available.")
		return
	}
	n, err := s.auth.RevokeAllTokens()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"revoked": n})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusNotFound, "The audit log is not available.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events":        s.auth.Audit(),
		"retentionDays": int(auth.AuditRetention / (24 * time.Hour)),
		"maxEvents":     auth.MaxAuditEvents,
	})
}

func (s *Server) handleVMs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.vmProbe.List(r.Context()))
}
