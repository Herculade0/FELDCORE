package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/bridge"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/health"
	"github.com/Herculade0/feldcore/internal/version"
	"github.com/Herculade0/feldcore/internal/vm"
)

// Phase 1 tools are read-only. There is no shell, no exec, and no tool that
// changes the host. Write tools are a later phase, behind an approval queue.

const mcpInstructions = "Read-only tools for this FELDCORE server. " +
	"They can look at the host, shares, containers, the App Store, virtual machines, health, and a configured Proton Bridge mailbox. " +
	"They cannot change anything, and there is no shell. " +
	"proton_bridge also needs the mail scope. It returns saved IMAP and SMTP details and does not sign in or stop Bridge."

func (s *Server) initMCP() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "feldcore",
		Version: version.Current,
	}, &mcp.ServerOptions{
		Instructions: mcpInstructions,
		Logger:       s.log,
	})
	server.AddReceivingMiddleware(s.mcpAudit)
	readonly := readOnlyAnnotations()
	mcp.AddTool(server, readonly("system_info", "System information",
		"Read hostname, operating system, kernel, CPU and boot time for this FELDCORE host."), s.toolSystemInfo)
	mcp.AddTool(server, readonly("system_stats", "System stats",
		"Read the latest CPU, memory, load, disk and network sample."), s.toolSystemStats)
	mcp.AddTool(server, readonly("list_shares", "List shares",
		"List file shares and whether SMB and NFS are enabled."), s.toolListShares)
	mcp.AddTool(server, readonly("list_containers", "List containers",
		"List Docker containers with image, state, status and published ports."), s.toolListContainers)
	mcp.AddTool(server, readonly("container_logs", "Container logs",
		"Read the last lines of one container's logs. This does not run a command inside the container."), s.toolContainerLogs)
	mcp.AddTool(server, readonly("list_apps", "List App Store apps",
		"List or search the App Store catalogue. Nothing is installed."), s.toolListApps)
	mcp.AddTool(server, readonly("list_vms", "List virtual machines",
		"List virtual machines when the VM layer is installed. Nothing is started or stopped."), s.toolListVMs)
	mcp.AddTool(server, readonly("health_report", "Health report",
		"Read the latest health checks: SMART, temperatures, disk space, load, containers, VMs, failed services, certificates, and tunnels. Nothing is changed."), s.toolHealthReport)
	mcp.AddTool(server, readonly("proton_bridge", "Proton Bridge mailbox",
		"Read the saved Proton Bridge username, app password, and IMAP and SMTP endpoints for AI integration. Requires the mail scope as well as read. Does not sign in, and does not stop or start the container."), s.toolProtonBridge)
	s.mcpHTTP = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Logger: s.log})
}

func readOnlyAnnotations() func(name, title, description string) *mcp.Tool {
	no := false
	return func(name, title, description string) *mcp.Tool {
		return &mcp.Tool{
			Name:        name,
			Description: description,
			Annotations: &mcp.ToolAnnotations{
				Title:           title,
				ReadOnlyHint:    true,
				DestructiveHint: &no,
				OpenWorldHint:   &no,
			},
		}
	}
}

type none struct{}

func toolCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 25*time.Second)
}

func (s *Server) toolSystemInfo(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
	if s.sys == nil {
		return nil, nil, errors.New("System information is not available.")
	}
	ctx, cancel := toolCtx(ctx)
	defer cancel()
	info, err := s.sys.Info(ctx)
	if err != nil {
		return nil, nil, errors.New("Couldn't read system information.")
	}
	return nil, info, nil
}

func (s *Server) toolHealthReport(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, health.Report, error) {
	if s.health == nil {
		return nil, health.Report{}, errors.New("Health monitoring is not available.")
	}
	return nil, s.health.Report(), nil
}

func (s *Server) toolProtonBridge(ctx context.Context, req *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, bridge.Status, error) {
	p, ok := s.principalForMCP(ctx, req)
	if !ok || !auth.Allows(p.Scopes, auth.ScopeMail) {
		return nil, bridge.Status{}, errors.New("This token needs the mail scope to read Bridge credentials. read alone is not enough. admin includes mail.")
	}
	ctx, cancel := toolCtx(ctx)
	defer cancel()
	st, err := s.bridgeStatus(ctx, "")
	if err != nil {
		return nil, bridge.Status{}, errors.New("Couldn't read the Proton Bridge mailbox.")
	}
	return nil, st, nil
}

func (s *Server) toolSystemStats(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
	if s.sampler == nil {
		return nil, nil, errors.New("No sample yet.")
	}
	st := s.sampler.Latest()
	if st == nil {
		return nil, nil, errors.New("No sample yet.")
	}
	return nil, st, nil
}

type shareBrief struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Comment string `json:"comment,omitempty"`
	SMB     proto  `json:"smb"`
	NFS     proto  `json:"nfs"`
}

type proto struct {
	Enabled  bool     `json:"enabled"`
	Access   string   `json:"access,omitempty"`
	Users    []string `json:"users,omitempty"`
	Clients  string   `json:"clients,omitempty"`
	ReadOnly bool     `json:"readOnly,omitempty"`
	State    string   `json:"state"`
}

type sharesOut struct {
	Root   string       `json:"root"`
	Shares []shareBrief `json:"shares"`
}

func (s *Server) toolListShares(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, sharesOut, error) {
	if s.shares == nil {
		return nil, sharesOut{}, errors.New("Shares are not available.")
	}
	ctx, cancel := toolCtx(ctx)
	defer cancel()
	ov := s.shares.Overview(ctx)
	out := sharesOut{Root: ov.Root, Shares: make([]shareBrief, 0, len(ov.Shares))}
	for _, sh := range ov.Shares {
		users := append([]string(nil), sh.SMB.Users...)
		out.Shares = append(out.Shares, shareBrief{
			Name: sh.Name, Path: sh.Path, Comment: sh.Comment,
			SMB: proto{
				Enabled: sh.SMB.Enabled, Access: sh.SMB.Access, Users: users, State: sh.SMBStatus.State,
			},
			NFS: proto{
				Enabled: sh.NFS.Enabled, Clients: sh.NFS.Clients, ReadOnly: sh.NFS.ReadOnly, State: sh.NFSStatus.State,
			},
		})
	}
	return nil, out, nil
}

type containerBrief struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Image   string        `json:"image"`
	State   string        `json:"state"`
	Status  string        `json:"status"`
	Ports   []docker.Port `json:"ports"`
	Network string        `json:"network,omitempty"`
}

type containersOut struct {
	Available  bool             `json:"available"`
	Message    string           `json:"message,omitempty"`
	Containers []containerBrief `json:"containers"`
}

func (s *Server) toolListContainers(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, containersOut, error) {
	if s.docker == nil {
		return nil, containersOut{Containers: []containerBrief{}}, errors.New("Docker is not available.")
	}
	ctx, cancel := toolCtx(ctx)
	defer cancel()
	st := s.docker.Status(ctx)
	if !st.Running {
		msg := st.Message
		if msg == "" {
			msg = "Docker is not running."
		}
		return nil, containersOut{Available: false, Message: msg, Containers: []containerBrief{}}, nil
	}
	cs, err := s.docker.Containers(ctx)
	if err != nil {
		return nil, containersOut{}, errors.New("Docker isn't reachable, so containers can't be listed.")
	}
	out := containersOut{Available: true, Containers: make([]containerBrief, 0, len(cs))}
	for _, c := range cs {
		ports := c.Ports
		if ports == nil {
			ports = []docker.Port{}
		}
		out.Containers = append(out.Containers, containerBrief{
			ID: c.ID, Name: c.Name, Image: c.Image, State: c.State, Status: c.Status,
			Ports: ports, Network: c.Network,
		})
	}
	return nil, out, nil
}

type logsIn struct {
	ID   string `json:"id" jsonschema:"container id or name"`
	Tail int    `json:"tail,omitempty" jsonschema:"how many lines to keep from the end"`
}

type logsOut struct {
	ID      string `json:"id"`
	Tail    int    `json:"tail"`
	Logs    string `json:"logs"`
	Trimmed bool   `json:"trimmed,omitempty"`
}

func (s *Server) toolContainerLogs(ctx context.Context, _ *mcp.CallToolRequest, in logsIn) (*mcp.CallToolResult, logsOut, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, logsOut{}, errors.New("id is required")
	}
	if s.docker == nil {
		return nil, logsOut{}, errors.New("Docker is not available.")
	}
	tail := in.Tail
	if tail <= 0 {
		tail = 100
	}
	if tail > 500 {
		tail = 500
	}
	ctx, cancel := toolCtx(ctx)
	defer cancel()
	logs, err := s.docker.Logs(ctx, id, tail)
	if err != nil {
		if docker.NotFound(err) {
			return nil, logsOut{}, errors.New("That container no longer exists.")
		}
		return nil, logsOut{}, errors.New("Couldn't read that container's logs.")
	}
	const maxLog = 48 << 10
	trimmed := false
	if len(logs) > maxLog {
		logs = logs[len(logs)-maxLog:]
		trimmed = true
	}
	return nil, logsOut{ID: id, Tail: tail, Logs: logs, Trimmed: trimmed}, nil
}

type appsIn struct {
	Query string `json:"query,omitempty" jsonschema:"text to match against the name author or description"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many apps to return at most"`
}

type appBrief struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Author     string   `json:"author,omitempty"`
	Image      string   `json:"image,omitempty"`
	Categories []string `json:"categories,omitempty"`
	Overview   string   `json:"overview,omitempty"`
}

type appsOut struct {
	Query   string     `json:"query,omitempty"`
	Total   int        `json:"total"`
	Apps    []appBrief `json:"apps"`
	Message string     `json:"message,omitempty"`
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

func (s *Server) toolListApps(ctx context.Context, _ *mcp.CallToolRequest, in appsIn) (*mcp.CallToolResult, appsOut, error) {
	if s.apps == nil {
		return nil, appsOut{}, errors.New("The App Store is not available.")
	}
	cat := s.apps.Catalogue()
	q := strings.ToLower(strings.TrimSpace(in.Query))
	limit := in.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	matched := make([]appBrief, 0)
	for _, a := range cat.Apps {
		overview := plainText(a.Overview, 240)
		if q != "" && !matchesApp(q, a.Name, a.Author, a.Image, overview, a.Categories) {
			continue
		}
		cats := append([]string(nil), a.Categories...)
		matched = append(matched, appBrief{
			ID: a.ID, Name: a.Name, Author: a.Author, Image: a.Image,
			Categories: cats, Overview: overview,
		})
	}
	sort.SliceStable(matched, func(i, j int) bool {
		return strings.ToLower(matched[i].Name) < strings.ToLower(matched[j].Name)
	})
	total := len(matched)
	if len(matched) > limit {
		matched = matched[:limit]
	}
	out := appsOut{Query: q, Total: total, Apps: matched, Message: cat.Message}
	if out.Apps == nil {
		out.Apps = []appBrief{}
	}
	return nil, out, nil
}

func matchesApp(q, name, author, image, overview string, categories []string) bool {
	hay := strings.ToLower(strings.Join(append([]string{name, author, image, overview}, categories...), " "))
	return strings.Contains(hay, q)
}

func plainText(s string, n int) string {
	s = htmlTag.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func (s *Server) toolListVMs(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, vm.List, error) {
	ctx, cancel := toolCtx(ctx)
	defer cancel()
	return nil, s.vmProbe.List(ctx), nil
}

func (s *Server) mcpAudit(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		p, ok := s.principalForMCP(ctx, req)
		if !ok {
			return res, err
		}
		action := "mcp " + method
		target := ""
		if method == "tools/call" {
			if name, tgt := toolCallDetail(req); name != "" {
				action = "mcp " + name
				target = tgt
			}
		}
		outcome := "ok"
		if err != nil {
			outcome = "error"
		} else if ctr, isTool := res.(*mcp.CallToolResult); isTool && ctr != nil && ctr.IsError {
			outcome = "error"
		}
		s.record(p, action, target, outcome)
		return res, err
	}
}

func (s *Server) principalForMCP(ctx context.Context, req mcp.Request) (principal, bool) {
	if p, ok := principalFrom(ctx); ok && p.ViaToken {
		return p, true
	}
	if s.auth == nil || req == nil || req.GetExtra() == nil {
		return principal{}, false
	}
	raw, ok := bearerToken(req.GetExtra().Header)
	if !ok {
		return principal{}, false
	}
	info, ok := s.auth.AuthenticateToken(raw)
	if !ok {
		return principal{}, false
	}
	return principal{ViaToken: true, TokenID: info.ID, TokenName: info.Name, Scopes: info.Scopes}, true
}

func toolCallDetail(req mcp.Request) (name, target string) {
	if req == nil || req.GetParams() == nil {
		return "", ""
	}
	raw, err := json.Marshal(req.GetParams())
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return "", ""
	}
	var body struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Name == "" {
		return "", ""
	}
	var args map[string]any
	_ = json.Unmarshal(body.Arguments, &args)
	for _, key := range []string{"id", "name", "query"} {
		if s, ok := args[key].(string); ok && strings.TrimSpace(s) != "" {
			return body.Name, strings.TrimSpace(s)
		}
	}
	return body.Name, ""
}
