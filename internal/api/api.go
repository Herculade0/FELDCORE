// Package api wires FELDCORE's HTTP surface: a versioned JSON API under
// /api/v1, a read-only MCP server at /mcp, plus the embedded single-page
// front-end served for everything else.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Herculade0/feldcore/internal/apps"
	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/backup"
	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/proxy"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/shares"
	"github.com/Herculade0/feldcore/internal/snapshots"
	"github.com/Herculade0/feldcore/internal/system"
	"github.com/Herculade0/feldcore/internal/vm"
	"github.com/Herculade0/feldcore/internal/vpn"
)

// Deps are the services the HTTP layer exposes.
type Deps struct {
	System     *system.Collector
	Sampler    *system.Sampler
	Auth       *auth.Service
	Citizen    *citizenid.Gateway
	Shares     *shares.Manager
	Docker     *docker.Manager
	Apps       *apps.Service
	VMs        *vm.Manager
	Setup      *setup.Service
	Remote     *remote.Service
	Proxy      *proxy.Service
	Snaps      *snapshots.Service
	SkipWizard bool
	WebRoot    fs.FS
	Log        *slog.Logger
	VPN        *vpn.Service
	Backups    *backup.Service
	Mail       *notify.Service
}

// Server holds the dependencies shared by HTTP handlers.
type Server struct {
	sys        *system.Collector
	sampler    *system.Sampler
	auth       *auth.Service
	citizen    *citizenid.Gateway
	shares     *shares.Manager
	docker     *docker.Manager
	apps       *apps.Service
	vms        *vm.Manager
	wiz        *setup.Service
	remote     *remote.Service
	proxy      *proxy.Service
	snaps      *snapshots.Service
	skipWizard bool
	webRoot    fs.FS
	log        *slog.Logger
	vmProbe    vm.Probe
	mcpHTTP    http.Handler
	vpn        *vpn.Service
	backups    *backup.Service
	mail       *notify.Service
}

// New returns a Server.
func New(d Deps) *Server {
	s := &Server{
		sys: d.System, sampler: d.Sampler, auth: d.Auth, citizen: d.Citizen, shares: d.Shares,
		docker: d.Docker, apps: d.Apps, vms: d.VMs, wiz: d.Setup,
		remote: d.Remote, proxy: d.Proxy, snaps: d.Snaps,
		skipWizard: d.SkipWizard, webRoot: d.WebRoot, log: d.Log,
		vpn: d.VPN, backups: d.Backups, mail: d.Mail,
	}
	s.initMCP()
	return s
}

// Router builds the full chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)

	short := middleware.Timeout(30 * time.Second)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(sameOrigin)

		// Open endpoints: health and the login flow itself.
		r.Group(func(r chi.Router) {
			r.Use(short)
			r.Get("/health", s.handleHealth)
			r.Get("/auth/status", s.handleAuthStatus)
			r.Post("/auth/setup", s.handleAuthSetup)
			r.Post("/auth/login", s.handleAuthLogin)
			r.Post("/auth/logout", s.handleAuthLogout)
			r.Post("/citizen/login", s.handleCitizenLogin)
			r.Post("/citizen/logout", s.handleCitizenLogout)
			r.Post("/citizen/webauthn/login/begin", s.handlePasskeyLoginBegin)
			r.Post("/citizen/webauthn/login/finish", s.handlePasskeyLoginFinish)
		})

		// Signed-in people, including accounts that are not the administrator.
		r.Group(func(r chi.Router) {
			r.Use(short)
			r.Use(s.requireUser)
			r.Get("/launcher", s.handleLauncher)
			r.Get("/citizen/me", s.handleCitizenMe)
			r.Post("/citizen/totp/begin", s.handleTOTPBegin)
			r.Post("/citizen/totp/confirm", s.handleTOTPConfirm)
			r.Post("/citizen/webauthn/register/begin", s.handlePasskeyRegisterBegin)
			r.Post("/citizen/webauthn/register/finish", s.handlePasskeyRegisterFinish)
			r.Delete("/citizen/passkeys/{id}", s.handlePasskeyDelete)
		})

		// Everything else needs a signed-in session or a scoped agent token.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Use(s.enforceScope)

			// Long-lived or slow endpoints sit outside the short timeout.
			r.Get("/system/stream", s.handleSystemStream)
			r.Get("/terminal", s.handleHostTerminal)
			r.Get("/docker/containers/{id}/terminal", s.handleContainerTerminal)
			r.Post("/shares/services/{service}/install", s.handleServiceInstall)
			// Installing Docker and pulling images can take minutes.
			r.Post("/docker/install", s.handleDockerInstall)
			r.Post("/docker/containers", s.handleDockerCreate)
			r.Put("/docker/containers/{id}", s.handleDockerUpdate)
			// The catalogue is about 20MB; a refresh must not sit under the
			// short request timeout.
			r.Post("/apps/refresh", s.handleAppsRefresh)
			r.Post("/setup/wizard", s.handleWizard)
			r.Put("/settings/services", s.handleSettingsService)
			r.Put("/settings/updates", s.handleSettingsUpdates)
			r.Put("/vpn", s.handleVPNEnabled)
			r.Post("/vpn/tunnels", s.handleVPNSave)
			r.Put("/vpn/tunnels/{id}", s.handleVPNSave)
			r.Post("/vpn/tunnels/{id}/enabled", s.handleVPNTunnelEnabled)
			r.Post("/backups/test", s.handleBackupsTest)
			r.Post("/backups/run", s.handleBackupsRun)
			r.Post("/notifications/test", s.handleNotificationsTest)
			// Defining a domain and the VNC console are not short requests.
			r.Post("/vms", s.handleVMCreate)
			r.Put("/vms/{name}", s.handleVMUpdate)
			r.Put("/vms/{name}/xml", s.handleVMXMLPut)
			r.Post("/vms/{name}/snapshots/{snap}/revert", s.handleVMSnapshotRevert)
			r.Get("/vms/{name}/console", s.handleVMConsole)
			r.Put("/remote", s.handleRemoteSave)
			r.Post("/remote/install", s.handleRemoteInstall)
			r.Put("/proxy", s.handleProxySave)
			r.Put("/docker/containers/{id}/address", s.handleAddressSave)
			r.Post("/docker/containers/{id}/snapshots", s.handleSnapshotCreate)
			r.Post("/docker/containers/{id}/snapshots/{snap}/restore", s.handleSnapshotRestore)
			r.Put("/snapshots", s.handleSnapshotSettings)

			r.Group(func(r chi.Router) {
				r.Use(short)
				r.Get("/system/info", s.handleSystemInfo)
				r.Get("/system/stats", s.handleSystemStats)
				r.Get("/system/history", s.handleSystemHistory)

				r.Get("/shares", s.handleSharesOverview)
				r.Post("/shares", s.handleShareCreate)
				r.Put("/shares/{name}", s.handleShareUpdate)
				r.Delete("/shares/{name}", s.handleShareDelete)

				r.Post("/shares/users", s.handleShareUserCreate)
				r.Put("/shares/users/{name}/password", s.handleShareUserPassword)
				r.Delete("/shares/users/{name}", s.handleShareUserDelete)

				r.Get("/fs/suggest", s.handleFSSuggest)

				r.Get("/setup/wizard", s.handleWizardStatus)

				r.Get("/apps", s.handleApps)
				r.Get("/apps/{id}", s.handleAppSpec)

				r.Get("/settings", s.handleSettings)
				r.Put("/settings/system", s.handleSettingsSystem)
				r.Put("/settings/network", s.handleSettingsNetwork)
				r.Put("/settings/firewall", s.handleSettingsFirewall)
				r.Put("/settings/password", s.handleSettingsPassword)
				r.Put("/settings/session", s.handleSettingsSession)
				r.Put("/settings/feed", s.handleSettingsFeed)
				r.Put("/settings/defaults", s.handleSettingsDefaults)
				r.Post("/settings/shares", s.handleSettingsShares)

				r.Get("/vpn", s.handleVPN)
				r.Post("/vpn/parse", s.handleVPNParse)
				r.Delete("/vpn/tunnels/{id}", s.handleVPNDelete)
				r.Get("/vpn/tunnels/{id}/status", s.handleVPNStatus)
				r.Get("/backups", s.handleBackups)
				r.Put("/backups", s.handleBackupsSave)
				r.Get("/notifications", s.handleNotifications)
				r.Put("/notifications", s.handleNotificationsSave)
				r.Get("/vms", s.handleVMs)
				r.Get("/vms/host", s.handleVMHost)
				r.Get("/vms/settings", s.handleVMSettings)
				r.Put("/vms/settings", s.handleVMSettingsSave)
				r.Get("/vms/virtio", s.handleVirtio)
				r.Post("/vms/virtio", s.handleVirtioDownload)
				r.Delete("/vms/virtio/{file}", s.handleVirtioDelete)
				r.Get("/vms/jobs/{id}", s.handleVMJob)
				r.Get("/vms/{name}", s.handleVMGet)
				r.Delete("/vms/{name}", s.handleVMDelete)
				r.Get("/vms/{name}/xml", s.handleVMXML)
				r.Post("/vms/{name}/start", s.handleVMStart)
				r.Post("/vms/{name}/stop", s.handleVMStop)
				r.Post("/vms/{name}/force-stop", s.handleVMForceStop)
				r.Post("/vms/{name}/pause", s.handleVMPause)
				r.Post("/vms/{name}/resume", s.handleVMResume)
				r.Post("/vms/{name}/autostart", s.handleVMAutostart)
				r.Get("/vms/{name}/snapshots", s.handleVMSnapshots)
				r.Post("/vms/{name}/snapshots", s.handleVMSnapshotCreate)
				r.Delete("/vms/{name}/snapshots/{snap}", s.handleVMSnapshotDelete)
				r.Get("/remote", s.handleRemote)
				r.Get("/proxy", s.handleProxy)
				r.Get("/proxy/ca", s.handleProxyCA)
				r.Get("/snapshots", s.handleSnapshotUsage)

				r.Get("/docker/status", s.handleDockerStatus)
				r.Get("/docker/options", s.handleDockerOptions)
				r.Get("/docker/containers", s.handleDockerContainers)
				r.Get("/docker/containers/{id}/spec", s.handleDockerSpec)
				r.Get("/docker/containers/{id}/address", s.handleAddress)
				r.Get("/docker/containers/{id}/snapshots", s.handleSnapshots)
				r.Delete("/docker/containers/{id}/snapshots/{snap}", s.handleSnapshotDelete)
				r.Get("/docker/images", s.handleDockerImages)
				r.Get("/docker/containers/{id}/logs", s.handleDockerLogs)

				r.Get("/audit", s.handleAudit)
				r.Get("/citizen", s.handleCitizenDirectory)
				r.Post("/citizen/users", s.handleCitizenUserCreate)
				r.Put("/citizen/users/{id}", s.handleCitizenUserUpdate)
				r.Post("/citizen/users/{id}/password", s.handleCitizenPassword)
				r.Post("/citizen/users/{id}/totp/reset", s.handleCitizenTOTPReset)
				r.Delete("/citizen/users/{id}", s.handleCitizenUserDelete)
				r.Delete("/citizen/users/{id}/passkeys/{key}", s.handleCitizenPasskeyDelete)
				r.Post("/citizen/groups", s.handleCitizenGroupCreate)
				r.Put("/citizen/groups/{id}", s.handleCitizenGroupUpdate)
				r.Delete("/citizen/groups/{id}", s.handleCitizenGroupDelete)
				r.Put("/citizen/apps/{container}", s.handleCitizenAppAccess)
				r.Get("/agent/tokens", s.handleTokensList)
				r.Post("/agent/tokens", s.handleTokenCreate)
				r.Post("/agent/tokens/revoke-all", s.handleTokensRevokeAll)
				r.Delete("/agent/tokens/{id}", s.handleTokenRevoke)

				r.Post("/docker/containers/{id}/start", s.handleDockerStart)
				r.Post("/docker/containers/{id}/stop", s.handleDockerStop)
				r.Post("/docker/containers/{id}/restart", s.handleDockerRestart)
				r.Delete("/docker/containers/{id}", s.handleDockerRemove)
			})
		})

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "No such API endpoint.")
		})
	})

	// Citizen ID pages and the proxy's forward_auth check. These sit outside
	// /api/v1 so a published app can reverse-proxy /citizen/* here.
	r.Get("/citizen/signin", s.handleCitizenPage)
	r.Post("/citizen/signin", s.handleCitizenPage)
	r.Get("/citizen/check", s.handleCitizenCheck)
	r.Post("/citizen/webauthn/login/begin", s.handlePasskeyLoginBegin)
	r.Post("/citizen/webauthn/login/finish", s.handlePasskeyLoginFinish)

	// Agents speak MCP here. Bearer token only; a browser session is not enough.
	if s.mcpHTTP != nil {
		mcpGate := s.requireBearer(auth.ScopeRead, s.mcpHTTP)
		r.Handle("/mcp", mcpGate)
		r.Handle("/mcp/", mcpGate)
	}

	// Everything else is the SPA (static assets, or index.html fallback).
	r.NotFound(s.handleSPA)
	return r
}

// securityHeaders sets conservative browser protections on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects state-changing requests that a browser sent from another
// site. Together with the SameSite=Strict session cookie this blocks CSRF.
// Non-browser clients send no Origin header and are unaffected.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(w, http.StatusForbidden, "Cross-site request refused.")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// readJSON decodes a small JSON request body into v.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return readJSONLimit(w, r, v, 64<<10)
}

// readJSONLimit is readJSON with a caller-chosen cap. Container specs from a
// large template are much bigger than the other request bodies.
func readJSONLimit(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "That request is too large.")
			return false
		}
		writeError(w, http.StatusBadRequest, "The request was not valid JSON.")
		return false
	}
	return true
}

// fail maps a service error onto an HTTP response.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var (
		authInput   *auth.InputError
		citInput    *citizenid.InputError
		shareInput  *shares.InputError
		shareOp     *shares.OpError
		dockerInput *docker.InputError
		dockerOp    *docker.OpError
		appsInput   *apps.InputError
		appsOp      *apps.OpError
		vmInput     *vm.InputError
		vmOp        *vm.OpError
		setupInput  *setup.InputError
		setupOp     *setup.OpError
		vpnInput    *vpn.InputError
		vpnOp       *vpn.OpError
		backupInput *backup.InputError
		backupOp    *backup.OpError
		mailInput   *notify.InputError
		mailOp      *notify.OpError
		remoteInput *remote.InputError
		remoteOp    *remote.OpError
		proxyInput  *proxy.InputError
		proxyOp     *proxy.OpError
		snapInput   *snapshots.InputError
		snapOp      *snapshots.OpError
	)
	switch {
	case errors.As(err, &authInput):
		writeError(w, http.StatusBadRequest, authInput.Msg)
	case errors.As(err, &citInput):
		writeError(w, http.StatusBadRequest, citInput.Msg)
	case errors.As(err, &shareInput):
		writeError(w, http.StatusBadRequest, shareInput.Msg)
	case errors.As(err, &dockerInput):
		writeError(w, http.StatusBadRequest, dockerInput.Msg)
	case errors.As(err, &appsInput):
		writeError(w, http.StatusBadRequest, appsInput.Msg)
	case errors.As(err, &vmInput):
		writeError(w, http.StatusBadRequest, vmInput.Msg)
	case errors.As(err, &setupInput):
		writeError(w, http.StatusBadRequest, setupInput.Msg)
	case errors.As(err, &vpnInput):
		writeError(w, http.StatusBadRequest, vpnInput.Msg)
	case errors.As(err, &backupInput):
		writeError(w, http.StatusBadRequest, backupInput.Msg)
	case errors.As(err, &mailInput):
		writeError(w, http.StatusBadRequest, mailInput.Msg)
	case errors.As(err, &remoteInput):
		writeError(w, http.StatusBadRequest, remoteInput.Msg)
	case errors.As(err, &proxyInput):
		writeError(w, http.StatusBadRequest, proxyInput.Msg)
	case errors.As(err, &snapInput):
		writeError(w, http.StatusBadRequest, snapInput.Msg)
	case errors.As(err, &shareOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, shareOp.Msg)
	case errors.As(err, &dockerOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, dockerOp.Msg)
	case errors.As(err, &appsOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, appsOp.Msg)
	case errors.As(err, &vmOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, vmOp.Msg)
	case errors.As(err, &setupOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, setupOp.Msg)
	case errors.As(err, &vpnOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, vpnOp.Msg)
	case errors.As(err, &backupOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, backupOp.Msg)
	case errors.As(err, &mailOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, mailOp.Msg)
	case errors.As(err, &remoteOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, remoteOp.Msg)
	case errors.As(err, &proxyOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, proxyOp.Msg)
	case errors.As(err, &snapOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, snapOp.Msg)
	case errors.Is(err, apps.ErrNotFound):
		writeError(w, http.StatusNotFound, "That app is not in the catalogue.")
	case docker.NotFound(err):
		writeError(w, http.StatusNotFound, "That container no longer exists.")
	case errors.Is(err, shares.ErrNotFound):
		writeError(w, http.StatusNotFound, "That item no longer exists.")
	case errors.Is(err, auth.ErrSetupDone):
		writeError(w, http.StatusConflict, "FELDCORE has already been set up. Sign in instead.")
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, citizenid.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "Incorrect username or password.")
	case errors.Is(err, citizenid.ErrInvalidCode), errors.Is(err, citizenid.ErrPasskey):
		writeError(w, http.StatusUnauthorized, "That verification could not be confirmed.")
	case errors.Is(err, citizenid.ErrDisabled):
		writeError(w, http.StatusForbidden, "This account is disabled.")
	case errors.Is(err, citizenid.ErrNotFound):
		writeError(w, http.StatusNotFound, "That item no longer exists.")
	case errors.Is(err, auth.ErrRateLimited), errors.Is(err, citizenid.ErrRateLimited), errors.Is(err, setup.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "Too many failed attempts. Wait a few minutes and try again.")
	default:
		s.log.Error("request failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "Something went wrong on the server. Check the FELDCORE log.")
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.sys.Info(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	st := s.sampler.Latest()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "No sample yet.")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleSystemHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sampler.History())
}

// handleSPA serves embedded front-end files, falling back to index.html so the
// client-side router owns unknown paths.
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if f, err := s.webRoot.Open(p); err == nil {
		f.Close()
		http.FileServer(http.FS(s.webRoot)).ServeHTTP(w, r)
		return
	}
	// SPA fallback.
	r2 := r.Clone(context.Background())
	r2.URL.Path = "/"
	http.StripPrefix("/", http.FileServer(http.FS(s.webRoot))).ServeHTTP(w, r2)
}
