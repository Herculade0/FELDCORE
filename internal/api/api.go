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
	"github.com/Herculade0/feldcore/internal/compose"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/files"
	"github.com/Herculade0/feldcore/internal/health"
	"github.com/Herculade0/feldcore/internal/install"
	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/osupdate"
	"github.com/Herculade0/feldcore/internal/proxy"
	"github.com/Herculade0/feldcore/internal/publish"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/shares"
	"github.com/Herculade0/feldcore/internal/snapshots"
	"github.com/Herculade0/feldcore/internal/storage"
	"github.com/Herculade0/feldcore/internal/store"
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
	Publish    *publish.Service
	OS         *osupdate.Service
	// DataDir and SharesRoot enable Compose stacks. Tests that leave DataDir
	// empty keep the previous API and get 404 for /stacks.
	DataDir    string
	SharesRoot string
	Storage    *storage.Service
	Health     *health.Service
	Alerts     *notify.Dispatcher
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
	health     *health.Service
	alerts     *notify.Dispatcher
	uploads    *files.Uploads
	fetcher    *files.Fetcher
	installs   *install.Runner
	publish    *publish.Service
	os         *osupdate.Service
	stacks     *compose.Service
	storage    *storage.Service
}

// New returns a Server.
func New(d Deps) *Server {
	s := &Server{
		sys: d.System, sampler: d.Sampler, auth: d.Auth, citizen: d.Citizen, shares: d.Shares,
		docker: d.Docker, apps: d.Apps, vms: d.VMs, wiz: d.Setup,
		remote: d.Remote, proxy: d.Proxy, snaps: d.Snaps,
		skipWizard: d.SkipWizard, webRoot: d.WebRoot, log: d.Log,
		vpn: d.VPN, backups: d.Backups, mail: d.Mail, publish: d.Publish,
		os:      d.OS,
		storage: d.Storage,
		health:  d.Health, alerts: d.Alerts,
	}
	if d.Backups != nil && d.Auth != nil {
		b, a := d.Backups, d.Auth
		b.Audit = func(actor, tokenID, action, target, result string) {
			if err := a.Record(tokenID, store.AuditEvent{
				TokenName: actor, Action: action, Target: clip(target, 200), Result: result,
			}); err != nil && d.Log != nil {
				d.Log.Warn("audit log write failed", "err", err)
			}
		}
	}
	if d.DataDir != "" {
		dataDir, sharesRoot := d.DataDir, d.SharesRoot
		s.stacks = compose.New(compose.Deps{
			DataDir: dataDir, SharesRoot: sharesRoot, Log: d.Log,
			Paths: func() compose.PathInfo {
				info := compose.PathInfo{SharesRoot: sharesRoot}
				if s.shares != nil {
					info.Shares = map[string]string{}
					for _, f := range s.shares.Folders() {
						info.Shares[strings.ToLower(f.Name)] = f.Path
					}
				}
				return info
			},
			Inspect: func(ctx context.Context, id string) (docker.SpecInfo, error) {
				if s.docker == nil {
					return docker.SpecInfo{}, &docker.OpError{Msg: "Docker is not available."}
				}
				return s.docker.Spec(ctx, id)
			},
			Remove: func(ctx context.Context, id string) error {
				if s.docker == nil {
					return &docker.OpError{Msg: "Docker is not available."}
				}
				_, err := s.docker.Remove(ctx, id, true, false)
				return err
			},
		})
	}
	s.uploads = files.NewUploads()
	s.fetcher = files.NewFetcher(func() bool {
		if s.auth == nil {
			return false
		}
		return s.auth.AllowLANURLs()
	})
	s.fetcher.OnSettle = s.settleFetch
	s.wireInstalls()
	if s.storage != nil {
		s.storage.SetAudit(s.auditStorage)
		s.storage.AttachJobs(s.installs)
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
			r.Get("/citizen/brand", s.handleCitizenBrand)
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

		// The wizard is open until an administrator exists, and then only
		// with that browser session. An agent token cannot run it. It stays
		// outside the short timeout because a disk step can take a while.
		r.Group(func(r chi.Router) {
			r.Use(s.wizardAccess)
			r.Get("/setup/wizard", s.handleWizardStatus)
			r.Post("/setup/wizard", s.handleWizard)
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
			r.Post("/docker/review", s.handleDockerReview)
			// The catalogue is about 20MB; a refresh must not sit under the
			// short request timeout.
			r.Post("/apps/refresh", s.handleAppsRefresh)
			r.Put("/settings/services", s.handleSettingsService)
			r.Put("/settings/updates", s.handleSettingsUpdates)
			r.Put("/vpn", s.handleVPNEnabled)
			r.Post("/vpn/tunnels", s.handleVPNSave)
			r.Put("/vpn/tunnels/{id}", s.handleVPNSave)
			r.Post("/vpn/tunnels/{id}/enabled", s.handleVPNTunnelEnabled)
			r.Post("/backups/test", s.handleBackupsTest)
			r.Post("/backups/run", s.handleBackupsRun)
			r.Get("/backup", s.handleBackupOverview)
			r.Get("/backup/jobs", s.handleBackupJobs)
			r.Get("/backup/jobs/{id}", s.handleBackupJob)
			r.Post("/backup/destinations", s.handleBackupCreate)
			r.Put("/backup/destinations/{id}", s.handleBackupUpdate)
			r.Delete("/backup/destinations/{id}", s.handleBackupDelete)
			r.Post("/backup/destinations/{id}/test", s.handleBackupTest)
			r.Post("/backup/destinations/{id}/run", s.handleBackupRun)
			r.Post("/backup/destinations/{id}/check", s.handleBackupCheck)
			r.Post("/backup/destinations/{id}/password", s.handleBackupPassword)
			r.Get("/backup/destinations/{id}/snapshots", s.handleBackupSnapshots)
			r.Get("/backup/destinations/{id}/snapshots/{snap}/files", s.handleBackupFiles)
			r.Post("/backup/restore", s.handleBackupRestore)
			r.Post("/notifications/test", s.handleNotificationsTest)
			r.Post("/notifications/channels/{id}/test", s.handleNotifyChannelTest)
			r.Post("/health/check", s.handleHealthCheck)
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
			r.Put("/publish", s.handlePublishSave)
			r.Put("/docker/containers/{id}/publish", s.handleContainerPublishSave)
			r.Post("/docker/containers/{id}/snapshots", s.handleSnapshotCreate)
			r.Post("/docker/containers/{id}/snapshots/{snap}/restore", s.handleSnapshotRestore)
			r.Put("/snapshots", s.handleSnapshotSettings)
			// Disk inventory runs smartctl, and creating a pool formats disks.
			// Both can outlive the short request timeout.
			r.Get("/storage", s.handleStorage)
			r.Get("/storage/disks", s.handleStorageDisks)
			r.Post("/storage/pools", s.handlePoolCreate)
			r.Post("/storage/pools/{name}/mount", s.handlePoolMount)
			r.Post("/storage/pools/{name}/unmount", s.handlePoolUnmount)
			r.Post("/storage/pools/{name}/destroy", s.handlePoolDestroy)
			r.Post("/storage/pools/{name}/disks", s.handlePoolAddDisk)
			r.Post("/storage/pools/{name}/replace", s.handlePoolReplace)
			r.Post("/storage/pools/{name}/jobs", s.handlePoolJob)
			r.Put("/storage/pools/{name}/schedule", s.handlePoolSchedule)
			r.Put("/storage/spindown", s.handleSpinDown)
			r.Post("/storage/spindown", s.handleSpinDownNow)
			r.Post("/storage/cache", s.handleStorageCache)
			r.Put("/storage/shares/{share}", s.handleStorageShare)

			// File downloads, archives and upload chunks can outlive the short timeout.
			r.Get("/shares/{name}/files/content", s.handleFileContent)
			r.Head("/shares/{name}/files/content", s.handleFileContent)
			r.Post("/shares/{name}/files/zip", s.handleFileZip)
			r.Post("/shares/{name}/uploads", s.handleUploadCreate)
			r.Patch("/shares/{name}/uploads/{id}", s.handleUploadPatch)
			// Cloning a repository can take longer than the short timeout.
			r.Post("/stacks/import", s.handleStackImport)

			r.Group(func(r chi.Router) {
				r.Use(short)
				r.Get("/system/info", s.handleSystemInfo)
				r.Get("/system/stats", s.handleSystemStats)
				r.Get("/system/history", s.handleSystemHistory)

				r.Get("/shares", s.handleSharesOverview)
				r.Post("/shares", s.handleShareCreate)
				r.Put("/shares/{name}", s.handleShareUpdate)
				r.Delete("/shares/{name}", s.handleShareDelete)

				r.Get("/shares/{name}/files", s.handleFileList)
				r.Post("/shares/{name}/files/mkdir", s.handleFileMkdir)
				r.Post("/shares/{name}/files/rename", s.handleFileRename)
				r.Post("/shares/{name}/files/move", s.handleFileMove)
				r.Post("/shares/{name}/files/copy", s.handleFileCopy)
				r.Post("/shares/{name}/files/delete", s.handleFileDelete)
				r.Get("/shares/{name}/uploads/{id}", s.handleUploadStatus)
				r.Delete("/shares/{name}/uploads/{id}", s.handleUploadAbort)
				r.Post("/shares/{name}/fetch", s.handleFetchStart)
				r.Get("/shares/{name}/fetch", s.handleFetchList)
				r.Get("/shares/{name}/fetch/{id}", s.handleFetchGet)
				r.Delete("/shares/{name}/fetch/{id}", s.handleFetchCancel)

				r.Post("/shares/users", s.handleShareUserCreate)
				r.Put("/shares/users/{name}/password", s.handleShareUserPassword)
				r.Delete("/shares/users/{name}", s.handleShareUserDelete)

				r.Get("/fs/suggest", s.handleFSSuggest)

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
				r.Get("/settings/files", s.handleFileSettingsGet)
				r.Put("/settings/files", s.handleFileSettingsPut)

				r.Get("/vpn", s.handleVPN)
				r.Post("/vpn/parse", s.handleVPNParse)
				r.Delete("/vpn/tunnels/{id}", s.handleVPNDelete)
				r.Get("/vpn/tunnels/{id}/status", s.handleVPNStatus)
				r.Get("/backups", s.handleBackups)
				r.Put("/backups", s.handleBackupsSave)
				r.Get("/notifications", s.handleNotifications)
				r.Put("/notifications", s.handleNotificationsSave)
				r.Post("/notifications/channels", s.handleNotifyChannelSave)
				r.Delete("/notifications/channels/{id}", s.handleNotifyChannelDelete)
				r.Put("/notifications/routes", s.handleNotifyRoutes)
				r.Put("/notifications/policy", s.handleNotifyPolicy)
				r.Get("/notifications/inbox", s.handleNotifyInbox)
				r.Post("/notifications/inbox/read", s.handleNotifyInboxRead)
				r.Get("/health/status", s.handleHealthReport)
				r.Get("/health/history", s.handleHealthHistory)
				r.Get("/health/thresholds", s.handleHealthThresholds)
				r.Put("/health/thresholds", s.handleHealthThresholdsSave)
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
				r.Get("/publish", s.handlePublish)
				r.Get("/docker/containers/{id}/publish", s.handleContainerPublish)
				r.Get("/docker/containers/{id}/snapshots", s.handleSnapshots)
				r.Delete("/docker/containers/{id}/snapshots/{snap}", s.handleSnapshotDelete)
				r.Get("/docker/images", s.handleDockerImages)
				r.Get("/docker/containers/{id}/logs", s.handleDockerLogs)

				r.Get("/installs", s.handleInstalls)
				r.Get("/installs/{id}", s.handleInstall)
				r.Post("/installs", s.handleInstallStart)

				r.Get("/os", s.handleOS)
				r.Post("/os/check", s.handleOSCheck)
				r.Post("/os/update", s.handleOSUpdate)
				r.Post("/os/rollback", s.handleOSRollback)
				r.Put("/os/schedule", s.handleOSSchedule)
				r.Put("/os/source", s.handleOSSource)
				r.Post("/os/sysexts/{id}", s.handleOSSysextInstall)
				r.Delete("/os/sysexts/{id}", s.handleOSSysextRemove)
				r.Post("/os/reboot", s.handleOSReboot)

				r.Get("/audit", s.handleAudit)
				r.Get("/help", s.handleHelp)
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
				r.Put("/citizen/defaults", s.handleCitizenDefaults)
				r.Get("/agent/tokens", s.handleTokensList)
				r.Post("/agent/tokens", s.handleTokenCreate)
				r.Post("/agent/tokens/revoke-all", s.handleTokensRevokeAll)
				r.Delete("/agent/tokens/{id}", s.handleTokenRevoke)

				r.Post("/docker/containers/{id}/start", s.handleDockerStart)
				r.Post("/docker/containers/{id}/stop", s.handleDockerStop)
				r.Post("/docker/containers/{id}/restart", s.handleDockerRestart)
				r.Delete("/docker/containers/{id}", s.handleDockerRemove)

				r.Get("/stacks", s.handleStacks)
				r.Post("/stacks", s.handleStackCreate)
				r.Post("/stacks/preview", s.handleStackPreview)
				r.Post("/stacks/from-container", s.handleStackFromContainer)
				r.Get("/stacks/jobs/{id}", s.handleStackJob)
				r.Get("/stacks/{name}", s.handleStackGet)
				r.Put("/stacks/{name}", s.handleStackSave)
				r.Post("/stacks/{name}/diff", s.handleStackDiff)
				r.Post("/stacks/{name}/deploy", s.handleStackDeploy)
				r.Post("/stacks/{name}/update", s.handleStackUpdate)
				r.Post("/stacks/{name}/stop", s.handleStackStop)
				r.Delete("/stacks/{name}", s.handleStackRemove)
				r.Get("/stacks/{name}/logs", s.handleStackLogs)
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
		status       *statusError
		authInput    *auth.InputError
		citInput     *citizenid.InputError
		shareInput   *shares.InputError
		shareOp      *shares.OpError
		dockerInput  *docker.InputError
		dockerOp     *docker.OpError
		appsInput    *apps.InputError
		appsOp       *apps.OpError
		vmInput      *vm.InputError
		vmOp         *vm.OpError
		setupInput   *setup.InputError
		setupOp      *setup.OpError
		vpnInput     *vpn.InputError
		vpnOp        *vpn.OpError
		backupInput  *backup.InputError
		backupOp     *backup.OpError
		mailInput    *notify.InputError
		mailOp       *notify.OpError
		healthInput  *health.InputError
		remoteInput  *remote.InputError
		remoteOp     *remote.OpError
		proxyInput   *proxy.InputError
		proxyOp      *proxy.OpError
		pubInput     *publish.InputError
		pubOp        *publish.OpError
		snapInput    *snapshots.InputError
		snapOp       *snapshots.OpError
		fileInput    *files.InputError
		fileOp       *files.OpError
		diskInput    *storage.InputError
		diskOp       *storage.OpError
		fileConflict *files.ConflictError
		fileForbid   *files.ForbiddenURL
		osInput      *osupdate.InputError
		osOp         *osupdate.OpError
		stackInput   *compose.InputError
		stackOp      *compose.OpError
		stackGate    *compose.Gate
	)
	switch {
	case errors.As(err, &status):
		writeError(w, status.code, status.msg)
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
	case errors.As(err, &healthInput):
		writeError(w, http.StatusBadRequest, healthInput.Msg)
	case errors.As(err, &remoteInput):
		writeError(w, http.StatusBadRequest, remoteInput.Msg)
	case errors.As(err, &proxyInput):
		writeError(w, http.StatusBadRequest, proxyInput.Msg)
	case errors.As(err, &pubInput):
		writeError(w, http.StatusBadRequest, pubInput.Msg)
	case errors.As(err, &snapInput):
		writeError(w, http.StatusBadRequest, snapInput.Msg)
	case errors.As(err, &osInput):
		writeError(w, http.StatusBadRequest, osInput.Msg)
	case errors.Is(err, osupdate.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &fileInput):
		writeError(w, http.StatusBadRequest, fileInput.Msg)
	case errors.As(err, &stackInput):
		writeError(w, http.StatusBadRequest, stackInput.Msg)
	case errors.As(err, &stackGate):
		if stackGate.Findings == nil {
			stackGate.Findings = []compose.Finding{}
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": stackGate.Error(), "findings": stackGate.Findings,
			"diff": stackGate.Diff, "confirm": stackGate.Confirm, "ack": stackGate.Ack,
		})
	case errors.As(err, &diskInput):
		writeError(w, http.StatusBadRequest, diskInput.Msg)
	case errors.As(err, &fileForbid):
		writeError(w, http.StatusBadRequest, fileForbid.Error())
	case errors.As(err, &fileConflict):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": fileConflict.Error(), "conflict": true,
			"name": fileConflict.Name, "path": fileConflict.Path,
		})
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
	case errors.As(err, &pubOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, pubOp.Msg)
	case errors.As(err, &snapOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, snapOp.Msg)
	case errors.As(err, &osOp):
		if s.log != nil {
			s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		}
		writeError(w, http.StatusInternalServerError, osOp.Msg)
	case errors.As(err, &diskOp):
		if s.log != nil {
			s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		}
		writeError(w, http.StatusInternalServerError, diskOp.Msg)
	case errors.As(err, &fileOp):
		if s.log != nil {
			s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		}
		writeError(w, http.StatusInternalServerError, fileOp.Msg)
	case errors.As(err, &stackOp):
		if s.log != nil {
			s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		}
		writeError(w, http.StatusInternalServerError, stackOp.Msg)
	case errors.Is(err, apps.ErrNotFound):
		writeError(w, http.StatusNotFound, "That app is not in the catalogue.")
	case docker.NotFound(err):
		writeError(w, http.StatusNotFound, "That container no longer exists.")
	case errors.Is(err, shares.ErrNotFound), errors.Is(err, files.ErrNotFound):
		writeError(w, http.StatusNotFound, "That item no longer exists.")
	case errors.Is(err, auth.ErrSetupDone):
		writeError(w, http.StatusConflict, "FELDCORE has already been set up. Sign in instead.")
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, citizenid.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "Incorrect username or password.")
	case errors.Is(err, auth.ErrTOTPRequired):
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "Enter the code from your authenticator.", "totpRequired": true,
		})
	case errors.Is(err, auth.ErrTOTPInvalid):
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "That authenticator code is not right.", "totpRequired": true,
		})
	case errors.Is(err, citizenid.ErrSecondFactor):
		writeError(w, http.StatusUnauthorized, "Enter the code from your authenticator.")
	case errors.Is(err, citizenid.ErrInvalidCode), errors.Is(err, citizenid.ErrPasskey):
		writeError(w, http.StatusUnauthorized, "That verification could not be confirmed.")
	case errors.Is(err, citizenid.ErrDisabled):
		writeError(w, http.StatusForbidden, "This account is disabled.")
	case errors.Is(err, citizenid.ErrNotFound):
		writeError(w, http.StatusNotFound, "That item no longer exists.")
	case errors.Is(err, auth.ErrRateLimited), errors.Is(err, citizenid.ErrRateLimited), errors.Is(err, setup.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "Too many failed attempts. Wait a few minutes and try again.")
	default:
		if s.log != nil {
			s.log.Error("request failed", "path", r.URL.Path, "err", err)
		}
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
