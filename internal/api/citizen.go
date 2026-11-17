package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/store"
)

func (s *Server) requireCitizen() bool { return s.citizen != nil }

func (s *Server) viewer(r *http.Request) (citizenid.Identity, bool) {
	if s.citizen == nil {
		return citizenid.Identity{}, false
	}
	if user, ok := s.currentUser(r); ok {
		if id, ok := s.citizen.IdentityForUsername(user); ok {
			return id, true
		}
	}
	return s.citizen.DeskIdentity(r)
}

// requireUser accepts the administrator session or any Citizen ID session.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.viewer(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "Sign in to continue.")
			return
		}
		ctx := citizenid.WithIdentity(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(withPrincipal(ctx, principal{Username: id.Username})))
	})
}

func (s *Server) handleCitizenPage(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		http.NotFound(w, r)
		return
	}
	s.citizen.ServeSignIn(w, r)
}

func (s *Server) handleCitizenCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		http.NotFound(w, r)
		return
	}
	s.citizen.ServeCheck(w, r)
}

func (s *Server) handleCitizenLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	id, err := s.citizen.Login(w, r, body.Username, body.Password, body.TOTP)
	if errors.Is(err, citizenid.ErrSecondFactor) {
		writeJSON(w, http.StatusOK, map[string]any{"totpRequired": true})
		return
	}
	// Recovery codes are issued by the setup wizard. Citizen ID does not know
	// them, so a wrong authenticator code is checked against that list once
	// the password has already matched.
	if errors.Is(err, citizenid.ErrInvalidCode) && s.auth != nil && s.auth.RedeemRecovery(strings.TrimSpace(body.Username), body.TOTP) {
		id, ok := s.citizen.IdentityForUsername(strings.TrimSpace(body.Username))
		if !ok || !id.Admin {
			s.fail(w, r, err)
			return
		}
		if err := s.issueDesk(w, r, id); err != nil {
			s.fail(w, r, err)
			return
		}
		_ = s.auth.Record("", store.AuditEvent{
			TokenName: id.Username,
			Action:    "sign-in",
			Target:    "recovery",
			Result:    "ok",
		})
		writeJSON(w, http.StatusOK, map[string]any{"username": id.Username, "admin": id.Admin})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.issueDesk(w, r, id); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": id.Username, "admin": id.Admin})
}

// issueDesk sets the host-only cookie the management UI uses, and the
// administrator session when this person is the administrator. /api/v1 does
// not read the parent-domain citizen cookie.
func (s *Server) issueDesk(w http.ResponseWriter, r *http.Request, id citizenid.Identity) error {
	if !s.managementHost(r) {
		// A desk cookie and an administrator session belong on this server's
		// own address. The parent-domain citizen cookie from this sign-in
		// still stands, including for the administrator on an app hostname.
		return nil
	}
	if err := s.citizen.StartDesk(w, r, id); err != nil {
		return err
	}
	if id.Admin && s.auth != nil {
		token, expires, err := s.auth.IssueSession(id.Username)
		if err != nil {
			return err
		}
		s.setSessionCookie(w, r, token, expires)
	}
	return nil
}

func readJSONAllowEmpty(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, "The request was not valid JSON.")
		return false
	}
	return true
}

// confirmFactor checks the signed-in caller's password and current
// authenticator before a factor is changed or removed.
func (s *Server) confirmFactor(w http.ResponseWriter, r *http.Request) bool {
	caller, ok := s.viewer(r)
	if !ok || s.citizen == nil {
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
		return false
	}
	var body struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if !readJSONAllowEmpty(w, r, &body) {
		return false
	}
	if err := s.citizen.ConfirmCaller(r, caller, body.Password, body.TOTP); err != nil {
		s.fail(w, r, err)
		return false
	}
	return true
}

func (s *Server) handleCitizenBrand(w http.ResponseWriter, r *http.Request) {
	if s.citizen == nil {
		writeJSON(w, http.StatusOK, store.SignInBrand{Title: "Citizen ID"})
		return
	}
	host := r.URL.Query().Get("host")
	if host == "" {
		host = r.Host
	}
	writeJSON(w, http.StatusOK, s.citizen.BrandForHost(host))
}

func (s *Server) handleCitizenDefaults(w http.ResponseWriter, r *http.Request) {
	if s.citizen == nil {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	var body store.SignInBrand
	if !readJSON(w, r, &body) {
		return
	}
	saved, err := s.citizen.SetBrand(body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleCitizenLogout(w http.ResponseWriter, r *http.Request) {
	if s.citizen != nil {
		_ = s.citizen.Logout(w, r)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

func (s *Server) handleLauncher(w http.ResponseWriter, r *http.Request) {
	id, ok := citizenid.FromContext(r.Context())
	if !ok || s.citizen == nil {
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": s.citizen.Launcher(id)})
}

func (s *Server) handleCitizenMe(w http.ResponseWriter, r *http.Request) {
	id, ok := citizenid.FromContext(r.Context())
	if !ok || s.citizen == nil {
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
		return
	}
	writeJSON(w, http.StatusOK, s.citizen.Profile(id))
}

func (s *Server) handleCitizenDirectory(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	writeJSON(w, http.StatusOK, s.citizen.Directory())
}

func (s *Server) handleCitizenUserCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	var body struct {
		Username    string   `json:"username"`
		DisplayName string   `json:"displayName"`
		Password    string   `json:"password"`
		GroupIDs    []string `json:"groupIds"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	info, err := s.citizen.CreateUser(body.Username, body.DisplayName, body.Password, body.GroupIDs)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleCitizenUserUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	var body struct {
		DisplayName *string   `json:"displayName"`
		Disabled    *bool     `json:"disabled"`
		GroupIDs    *[]string `json:"groupIds"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	info, err := s.citizen.UpdateUser(chi.URLParam(r, "id"), citizenid.UserPatch{
		DisplayName: body.DisplayName, Disabled: body.Disabled, GroupIDs: body.GroupIDs,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleCitizenPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.citizen.SetPassword(chi.URLParam(r, "id"), body.Password); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) handleCitizenTOTPReset(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	if !s.confirmFactor(w, r) {
		return
	}
	user, _ := s.currentUser(r)
	old := s.sessionCookieValue(r)
	id := chi.URLParam(r, "id")
	if err := s.citizen.ResetTOTP(id); err != nil {
		s.fail(w, r, err)
		return
	}
	if id == "admin" {
		s.restoreCallerSession(w, r, user, old)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (s *Server) handleCitizenUserDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	if err := s.citizen.DeleteUser(chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) handleCitizenGroupCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	info, err := s.citizen.CreateGroup(body.Name, body.Description)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleCitizenGroupUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	info, err := s.citizen.UpdateGroup(chi.URLParam(r, "id"), body.Name, body.Description)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleCitizenGroupDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	if err := s.citizen.DeleteGroup(chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) handleCitizenAppAccess(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	var body struct {
		Level    string   `json:"level"`
		GroupIDs []string `json:"groupIds"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.citizen.SetAppAccess(chi.URLParam(r, "container"), body.Level, body.GroupIDs); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.proxy != nil {
		if err := s.proxy.Apply(r.Context()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleTOTPBegin(w http.ResponseWriter, r *http.Request) {
	id, ok := citizenid.FromContext(r.Context())
	if !ok || s.citizen == nil {
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
		return
	}
	if s.citizen.FactorOn(id) {
		var body struct {
			Password string `json:"password"`
			TOTP     string `json:"totp"`
		}
		if !readJSONAllowEmpty(w, r, &body) {
			return
		}
		if err := s.citizen.ConfirmCaller(r, id, body.Password, body.TOTP); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	setup, err := s.citizen.BeginTOTP(id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, setup)
}

func (s *Server) handleTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	id, ok := citizenid.FromContext(r.Context())
	if !ok || s.citizen == nil {
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.citizen.ConfirmTOTP(id, body.Code); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"totp": true})
}

func (s *Server) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	opts, err := s.citizen.BeginPasskeyLogin(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

func (s *Server) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	id, err := s.citizen.FinishPasskeyLogin(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// A passkey signs the person into the app. It does not mint a desk or
	// administrator session, on this hostname or any other.
	writeJSON(w, http.StatusOK, map[string]any{"username": id.Username, "admin": id.Admin})
}

func (s *Server) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	id, ok := citizenid.FromContext(r.Context())
	if !ok || s.citizen == nil {
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
		return
	}
	if !s.confirmFactor(w, r) {
		return
	}
	opts, err := s.citizen.BeginPasskeyRegister(w, r, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

func (s *Server) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	id, ok := citizenid.FromContext(r.Context())
	if !ok || s.citizen == nil {
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
		return
	}
	if err := s.citizen.FinishPasskeyRegister(w, r, id); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": s.citizen.Profile(id).Passkeys})
}

func (s *Server) handlePasskeyDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := citizenid.FromContext(r.Context())
	if !ok || s.citizen == nil {
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
		return
	}
	if !s.confirmFactor(w, r) {
		return
	}
	if err := s.citizen.DeletePasskey(id.ID, chi.URLParam(r, "id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) handleCitizenPasskeyDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireCitizen() {
		writeError(w, http.StatusNotFound, "Citizen ID is not available.")
		return
	}
	if !s.confirmFactor(w, r) {
		return
	}
	if err := s.citizen.DeletePasskey(chi.URLParam(r, "id"), chi.URLParam(r, "key")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}
