// Package bridge describes the Proton Mail Bridge app: the headless IMAP and
// SMTP endpoints other FELDCORE apps use, and the interactive login that has
// to happen in the container.
package bridge

import (
	"errors"
	"strings"

	"github.com/Herculade0/feldcore/internal/notify"
)

const (
	// AppID is the store template id and the container label value.
	AppID = "proton-bridge"
	// Purpose is what this app is for. It is stored on the container and shown
	// in the App Store and on the mailbox panel.
	Purpose = "AI integration"
	// Image is the repository without a tag. ImageRef is what the template runs.
	Image    = "shenxn/protonmail-bridge"
	ImageRef = "shenxn/protonmail-bridge:latest"
	// SecretName is the encrypted file that holds the Bridge username and
	// app password. It is not the Proton account password.
	SecretName = "proton-bridge-mail"
	// InfoScript prints the Bridge CLI info and exits. It does not log in.
	InfoScript = "printf 'info\\nexit\\n' | protonmail-bridge --cli"
)

// ErrNeedsLogin means the CLI did not print a mailbox. The account still
// has to be added from the console.
var ErrNeedsLogin = errors.New("bridge has no account")

// ErrBusy means another Bridge process holds the keychain.
var ErrBusy = errors.New("bridge is already running")

// Mailbox is one address Bridge will accept on IMAP and SMTP.
type Mailbox struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Credentials is what other apps use. Username and Password are the first
// mailbox. Mailboxes lists every address info printed, which matters in
// split-address mode.
type Credentials struct {
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	Mailboxes []Mailbox `json:"mailboxes"`
}

// Candidate is a container that might be this app.
type Candidate struct {
	ID    string
	Name  string
	Image string
	State string
	App   string
}

// Published is one host port taken from the container spec.
type Published struct {
	HostIP        string
	HostPort      int
	ContainerPort int
	Proto         string
}

// Endpoint is an address other apps can open.
type Endpoint struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Bind     string `json:"bind,omitempty"`
	Security string `json:"security"`
	Insecure bool   `json:"insecure"`
	Note     string `json:"note,omitempty"`
}

// LoginGuide is the console sequence. Proton asks for a password and a 2FA
// code, so this cannot be submitted as an API call.
type LoginGuide struct {
	Steps    []string `json:"steps"`
	Commands []string `json:"commands"`
	After    []string `json:"after"`
}

// Status is the mailbox panel and the API other apps call.
type Status struct {
	Purpose       string     `json:"purpose"`
	Image         string     `json:"image"`
	Installed     bool       `json:"installed"`
	Running       bool       `json:"running"`
	ContainerID   string     `json:"containerId,omitempty"`
	ContainerName string     `json:"containerName,omitempty"`
	Configured    bool       `json:"configured"`
	NeedsLogin    bool       `json:"needsLogin"`
	Username      string     `json:"username,omitempty"`
	Password      string     `json:"password,omitempty"`
	Mailboxes     []Mailbox  `json:"mailboxes"`
	IMAP          Endpoint   `json:"imap"`
	SMTP          Endpoint   `json:"smtp"`
	Plan          string     `json:"plan"`
	Intro         string     `json:"intro"`
	Clients       string     `json:"clients"`
	Login         LoginGuide `json:"login"`
	Reauth        []string   `json:"reauth"`
	Notice        string     `json:"notice,omitempty"`
}

// InputError is a validation failure safe to show the user.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// Match reports whether a container is the store app. The notification
// Bridge, which only publishes SMTP on 127.0.0.1, is left out.
func Match(c Candidate) bool {
	if c.Name == notify.BridgeName {
		return false
	}
	if c.App == AppID || c.Name == AppID {
		return true
	}
	return ImageMatch(c.Image)
}

// ImageMatch reports whether ref is shenxn/protonmail-bridge, with or without
// a registry prefix, tag, or digest.
func ImageMatch(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "sha256:") {
		return false
	}
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	ref = strings.TrimPrefix(ref, "docker.io/")
	ref = strings.TrimPrefix(ref, "index.docker.io/")
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		ref = ref[:colon]
	}
	return ref == Image
}

// Pick chooses the store container when several match. A labelled container
// wins, then one named proton-bridge, then any other copy of the image. A
// running container wins inside each group.
func Pick(all []Candidate) (Candidate, bool) {
	var labeled, named, imaged []Candidate
	for _, c := range all {
		if c.Name == notify.BridgeName {
			continue
		}
		switch {
		case c.App == AppID:
			labeled = append(labeled, c)
		case c.Name == AppID:
			named = append(named, c)
		case ImageMatch(c.Image):
			imaged = append(imaged, c)
		}
	}
	for _, group := range [][]Candidate{labeled, named, imaged} {
		if c, ok := preferRunning(group); ok {
			return c, true
		}
	}
	return Candidate{}, false
}

func preferRunning(group []Candidate) (Candidate, bool) {
	if len(group) == 0 {
		return Candidate{}, false
	}
	for _, c := range group {
		if c.State == "running" {
			return c, true
		}
	}
	return group[0], true
}

// ParseInfo reads the text the Bridge CLI prints for info.
func ParseInfo(text string) ([]Mailbox, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil, ErrNeedsLogin
	}
	lowAll := strings.ToLower(text)
	if strings.Contains(lowAll, "another instance") || strings.Contains(lowAll, "failed to create lock") {
		return nil, ErrBusy
	}
	var out []Mailbox
	var cur *Mailbox
	section := ""
	flush := func() {
		if cur == nil {
			return
		}
		cur.Username = strings.TrimSpace(cur.Username)
		cur.Password = strings.TrimSpace(cur.Password)
		if cur.Username != "" && cur.Password != "" {
			out = append(out, *cur)
		}
		cur = nil
		section = ""
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, ">>>")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		switch {
		case strings.HasPrefix(low, "configuration for "):
			flush()
			cur = &Mailbox{}
		case strings.HasPrefix(low, "imap settings"):
			section = "imap"
		case strings.HasPrefix(low, "smtp settings"):
			section = "smtp"
		case strings.HasPrefix(low, "username:"):
			if cur == nil {
				cur = &Mailbox{}
			}
			user := strings.TrimSpace(line[len("Username:"):])
			if cur.Username == "" || section == "imap" {
				cur.Username = user
			}
		case strings.HasPrefix(low, "password:"):
			if cur == nil {
				cur = &Mailbox{}
			}
			pass := strings.TrimSpace(line[len("Password:"):])
			if cur.Password == "" || section == "imap" {
				cur.Password = pass
			}
		}
	}
	flush()
	if len(out) == 0 {
		return nil, ErrNeedsLogin
	}
	return out, nil
}

// FromMailboxes builds the stored credential record.
func FromMailboxes(boxes []Mailbox) (Credentials, error) {
	if len(boxes) == 0 || boxes[0].Username == "" || boxes[0].Password == "" {
		return Credentials{}, ErrNeedsLogin
	}
	clean := make([]Mailbox, len(boxes))
	copy(clean, boxes)
	return Credentials{Username: clean[0].Username, Password: clean[0].Password, Mailboxes: clean}, nil
}

// Validate checks a username and app password typed into the panel.
func Validate(username, password string) (Credentials, error) {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || strings.ContainsAny(username, " \t\r\n") || len(username) > 200 {
		return Credentials{}, &InputError{Msg: "Enter the Bridge username from the info command."}
	}
	if password == "" || strings.ContainsAny(password, "\r\n") || len(password) > 200 {
		return Credentials{}, &InputError{Msg: "Enter the Bridge password from the info command."}
	}
	return FromMailboxes([]Mailbox{{Username: username, Password: password}})
}

// ViewInput is everything the panel needs besides the fixed guide text.
type ViewInput struct {
	Found    bool
	ID       string
	Name     string
	Image    string
	State    string
	Hostname string
	Ports    []Published
	Creds    *Credentials
	Notice   string
	Multiple bool
}

// View assembles the panel. Ports come from the container; the password comes
// from the encrypted store, which is filled after info or a paste.
func View(in ViewInput) Status {
	st := Status{
		Purpose:   Purpose,
		Image:     ImageRef,
		Mailboxes: []Mailbox{},
		Plan:      PlanText,
		Intro:     IntroText,
		Clients:   ClientsText,
		Login:     LoginGuideText(),
		Reauth:    ReauthSteps(),
		Notice:    in.Notice,
	}
	if in.Image != "" {
		st.Image = in.Image
	}
	st.IMAP = endpoint(in.Ports, 143, in.Hostname, "IMAP is not published. Edit the container and map host port 1143 to container port 143.")
	st.SMTP = endpoint(in.Ports, 25, in.Hostname, "SMTP is not published. Edit the container and map host port 1025 to container port 25.")
	if in.Creds != nil && in.Creds.Username != "" && in.Creds.Password != "" {
		st.Configured = true
		st.Username = in.Creds.Username
		st.Password = in.Creds.Password
		st.Mailboxes = append([]Mailbox(nil), in.Creds.Mailboxes...)
		if len(st.Mailboxes) == 0 {
			st.Mailboxes = []Mailbox{{Username: in.Creds.Username, Password: in.Creds.Password}}
		}
	}
	st.NeedsLogin = !st.Configured
	if !in.Found {
		if st.Notice == "" {
			st.Notice = "Install Proton Bridge from the App Store. The template maps a data folder, publishes IMAP and SMTP, and restarts unless you stop it."
		}
		return st
	}
	st.Installed = true
	st.Running = in.State == "running"
	st.ContainerID = in.ID
	st.ContainerName = in.Name
	if in.Multiple && st.Notice == "" {
		st.Notice = "More than one Bridge container is present. This page uses " + in.Name + "."
	}
	return st
}

func endpoint(ports []Published, containerPort int, hostname, missing string) Endpoint {
	ep := Endpoint{Security: "starttls", Insecure: true, Note: missing}
	var found *Published
	for i := range ports {
		p := &ports[i]
		if p.ContainerPort == containerPort && (p.Proto == "" || p.Proto == "tcp") && p.HostPort > 0 {
			found = p
			break
		}
	}
	if found == nil {
		return ep
	}
	host, bind := advertise(found.HostIP, hostname)
	ep.Host = host
	ep.Port = found.HostPort
	ep.Bind = bind
	ep.Note = bindNote(bind)
	return ep
}

func advertise(bind, hostname string) (host, shown string) {
	bind = strings.TrimSpace(bind)
	if bind == "" || bind == "0.0.0.0" || bind == "::" {
		if strings.TrimSpace(hostname) == "" {
			return "this server", ""
		}
		return strings.TrimSpace(hostname), ""
	}
	return bind, bind
}

func bindNote(bind string) string {
	if bind == "127.0.0.1" || bind == "::1" {
		return "Bound to this server only. Programs on the server use " + bind + ". Another container's 127.0.0.1 is that container, so it cannot use this port."
	}
	return "Published on the LAN. Clients use this server's address and the port above. Another container reaches it through the host address, not through its own 127.0.0.1."
}

const (
	PlanText    = "Proton Mail Bridge needs a paid plan: Mail Plus, Unlimited, Family, Duo, or Visionary. A Proton Free account cannot use Bridge."
	IntroText   = "This app's purpose is AI integration. Other FELDCORE apps and bots connect to the IMAP and SMTP endpoints with the Bridge username and app password."
	ClientsText = "Use the host and port shown here. The info command prints 127.0.0.1 with ports 1143 and 1025, which are inside the container. This image forwards host port 1143 to container port 143, and host port 1025 to container port 25. Bridge presents its own certificate, so clients skip certificate checks for these ports."
)

// LoginGuideText is the first-run sequence. The commands match the
// shenxn/protonmail-bridge entrypoint: init builds the keychain, then the
// CLI accepts login.
func LoginGuideText() LoginGuide {
	return LoginGuide{
		Steps: []string{
			"Start the container if it is stopped. The console is available while it is running.",
			"Open Console. That is a shell inside the container, the same as docker exec.",
			"Bridge is already running, and only one copy can use the keychain. Run the commands below. They stop Bridge and the port forward, then start the image's init login. On a new data folder, init creates the GPG key and the pass keychain, then opens the Bridge CLI. If init says the key already exists, run protonmail-bridge --cli instead.",
			"At the >>> prompt, type login. Enter the Proton username, the account password, and the 2FA code when Proton asks. An account with a mailbox password gets one more prompt.",
			"Type info. The username and password in that printout are the Bridge credentials. The password is generated by Bridge.",
			"Type exit, then restart the container from the Docker page. The data folder keeps the login.",
			"Come back to Mailbox access and choose Read from Bridge, or paste the username and password from info.",
		},
		Commands: []string{
			"pkill -x protonmail-bridge || true",
			"pkill -x socat || true",
			"/protonmail/entrypoint.sh init",
		},
		After: []string{
			"login",
			"info",
			"exit",
		},
	}
}

// ReauthSteps is the path when Proton invalidates the Bridge session.
func ReauthSteps() []string {
	return []string{
		"Proton sometimes requires a new sign-in. Leave the data folder in place.",
		"Open Console and stop Bridge with the same pkill commands as the first login.",
		"Run protonmail-bridge --cli. If the account is already listed, type logout, then login, and complete the password and 2FA prompts again.",
		"Type info and exit, restart the container, and read the credentials again. Do that whenever info shows a new password.",
	}
}
