package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/bridge"
	"github.com/Herculade0/feldcore/internal/docker"
)

func (s *Server) handleBridge(w http.ResponseWriter, r *http.Request) {
	st, err := s.bridgeStatus(r.Context(), r.URL.Query().Get("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleBridgeSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSONLimit(w, r, &body, 1<<16) {
		return
	}
	creds, err := bridge.Validate(body.Username, body.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.storeBridge(creds); err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.bridgeStatus(r.Context(), r.URL.Query().Get("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleBridgeRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !readJSONLimit(w, r, &body, 1<<16) {
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 90*time.Second)
	defer cancel()
	st, err := s.readBridge(ctx, r, body.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) bridgeStatus(ctx context.Context, id string) (bridge.Status, error) {
	in := bridge.ViewInput{Hostname: s.bridgeHost(ctx), Creds: s.loadBridge()}
	c, multiple, err := s.locateBridge(ctx, id)
	if err != nil {
		var input *docker.InputError
		if errors.As(err, &input) {
			return bridge.Status{}, err
		}
		if id != "" {
			return bridge.Status{}, &docker.OpError{Msg: "Docker is not available."}
		}
		in.Notice = "Docker is not available, so the Bridge container cannot be seen from here."
		return bridge.View(in), nil
	}
	if c.ID == "" {
		return bridge.View(in), nil
	}
	in.Found = true
	in.Multiple = multiple
	in.ID = c.ID
	in.Name = c.Name
	in.Image = c.Image
	in.State = c.State
	if s.docker != nil {
		if info, specErr := s.docker.Spec(ctx, c.ID); specErr == nil {
			if info.Spec.Image != "" && !strings.HasPrefix(info.Spec.Image, "sha256:") {
				in.Image = info.Spec.Image
			}
			in.Ports = publishedPorts(info.Spec)
		}
	}
	return bridge.View(in), nil
}

func (s *Server) bridgeHost(ctx context.Context) string {
	if s.sys == nil {
		return ""
	}
	info, err := s.sys.Info(ctx)
	if err != nil || info == nil {
		return ""
	}
	return info.Hostname
}

func (s *Server) locateBridge(ctx context.Context, id string) (docker.Container, bool, error) {
	if s.docker == nil {
		if id != "" {
			return docker.Container{}, false, &docker.OpError{Msg: "Docker is not available."}
		}
		return docker.Container{}, false, nil
	}
	cs, err := s.docker.Containers(ctx)
	if err != nil {
		return docker.Container{}, false, err
	}
	id = strings.TrimSpace(id)
	n := 0
	var picked docker.Container
	all := make([]bridge.Candidate, 0, len(cs))
	by := map[string]docker.Container{}
	for _, c := range cs {
		cand := bridge.Candidate{ID: c.ID, Name: c.Name, Image: c.Image, State: c.State, App: c.App}
		if bridge.Match(cand) {
			n++
		}
		all = append(all, cand)
		by[c.ID] = c
		if id != "" && (c.ID == id || c.Name == id) {
			picked = c
		}
	}
	if id != "" {
		if picked.ID == "" {
			return docker.Container{}, false, &docker.InputError{Msg: "That container is not on this server."}
		}
		if !bridge.Match(bridge.Candidate{ID: picked.ID, Name: picked.Name, Image: picked.Image, State: picked.State, App: picked.App}) {
			return docker.Container{}, false, &docker.InputError{Msg: "That container is not Proton Bridge."}
		}
		return picked, n > 1, nil
	}
	choice, ok := bridge.Pick(all)
	if !ok {
		return docker.Container{}, false, nil
	}
	return by[choice.ID], n > 1, nil
}

func publishedPorts(spec docker.Spec) []bridge.Published {
	var out []bridge.Published
	for _, c := range spec.Configs {
		if c.Type != docker.TypePort {
			continue
		}
		out = append(out, bridge.Published{
			HostIP: c.HostIP, HostPort: c.HostPort, ContainerPort: c.ContainerPort, Proto: c.Proto,
		})
	}
	return out
}

func (s *Server) loadBridge() *bridge.Credentials {
	if s.secrets == nil {
		return nil
	}
	raw, ok, err := s.secrets.Get(bridge.SecretName)
	if err != nil || !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	var creds bridge.Credentials
	if json.Unmarshal([]byte(raw), &creds) != nil || creds.Username == "" || creds.Password == "" {
		return nil
	}
	if len(creds.Mailboxes) == 0 {
		creds.Mailboxes = []bridge.Mailbox{{Username: creds.Username, Password: creds.Password}}
	}
	return &creds
}

func (s *Server) storeBridge(creds bridge.Credentials) error {
	if s.secrets == nil {
		return &docker.OpError{Msg: "Credential storage is not available."}
	}
	if len(creds.Mailboxes) == 0 {
		creds.Mailboxes = []bridge.Mailbox{{Username: creds.Username, Password: creds.Password}}
	}
	raw, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	if err := s.secrets.Put(bridge.SecretName, string(raw)); err != nil {
		return &docker.OpError{Msg: "Could not store the Bridge password."}
	}
	return nil
}

func (s *Server) readBridge(ctx context.Context, r *http.Request, id string) (bridge.Status, error) {
	if s.docker == nil {
		return bridge.Status{}, &docker.OpError{Msg: "Docker is not available."}
	}
	c, _, err := s.locateBridge(ctx, id)
	if err != nil {
		var input *docker.InputError
		if errors.As(err, &input) {
			return bridge.Status{}, err
		}
		return bridge.Status{}, &docker.OpError{Msg: "Docker is not available."}
	}
	if c.ID == "" {
		return bridge.Status{}, &docker.InputError{Msg: "Install Proton Bridge from the App Store before reading credentials."}
	}
	info, err := s.docker.Spec(ctx, c.ID)
	if err != nil {
		return bridge.Status{}, &docker.OpError{Msg: "Could not read the Bridge container."}
	}
	bind, ok := rootBind(info.Spec)
	if !ok {
		return bridge.Status{}, &docker.InputError{Msg: "The Bridge data folder is not mounted at /root, so the login cannot be read."}
	}
	image := c.Image
	if info.Spec.Image != "" && !strings.HasPrefix(info.Spec.Image, "sha256:") {
		image = info.Spec.Image
	}
	wasRunning := c.State == "running"
	if wasRunning {
		s.quietName(r, c.Name)
		if err := s.docker.Stop(ctx, c.ID); err != nil {
			return bridge.Status{}, &docker.OpError{Msg: "Could not stop Bridge long enough to read the login."}
		}
	}
	bringUp := wasRunning
	defer func() {
		if !bringUp {
			return
		}
		startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.docker.Start(startCtx, c.ID)
	}()
	logs, runErr := s.docker.RunScript(ctx, image, []string{bind}, bridge.InfoScript)
	boxes, parseErr := bridge.ParseInfo(logs)
	if parseErr != nil || (runErr != nil && len(boxes) == 0) {
		notice := "Bridge did not report an account. Sign in from the console, then read again."
		if errors.Is(parseErr, bridge.ErrBusy) {
			notice = "Bridge was still using the keychain. Restart the container, then read again."
		}
		st, statusErr := s.bridgeStatus(ctx, c.ID)
		if statusErr != nil {
			st = bridge.View(bridge.ViewInput{Found: true, ID: c.ID, Name: c.Name, Image: image, Creds: s.loadBridge()})
		}
		st.Notice = notice
		return st, nil
	}
	creds, err := bridge.FromMailboxes(boxes)
	if err != nil {
		return bridge.Status{}, &docker.InputError{Msg: "Bridge did not report an account. Sign in from the console, then read again."}
	}
	if err := s.storeBridge(creds); err != nil {
		return bridge.Status{}, err
	}
	bringUp = false
	if err := s.docker.Start(ctx, c.ID); err != nil {
		st, _ := s.bridgeStatus(ctx, c.ID)
		st.Notice = "The Bridge password was saved. Start the container from the Docker page so IMAP and SMTP answer."
		return st, nil
	}
	st, err := s.bridgeStatus(ctx, c.ID)
	if err != nil {
		return bridge.Status{}, err
	}
	if st.Notice == "" {
		st.Notice = "Bridge credentials are saved. Other apps can use the IMAP and SMTP endpoints on this page."
	}
	return st, nil
}

func rootBind(spec docker.Spec) (string, bool) {
	for _, c := range spec.Configs {
		if c.Type != docker.TypePath || c.Container != "/root" || strings.TrimSpace(c.Host) == "" {
			continue
		}
		mode := c.Mode
		if mode == "" {
			mode = "rw"
		}
		return c.Host + ":/root:" + mode, true
	}
	return "", false
}
