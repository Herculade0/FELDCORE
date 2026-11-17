package bridge

import (
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/notify"
)

const infoText = `
>>> info
Configuration for ada@proton.me
IMAP Settings
Address:   127.0.0.1
IMAP port: 1143
Username:  ada@proton.me
Password:  bridge-app-password
Security:  STARTTLS

SMTP Settings
Address:   127.0.0.1
SMTP port: 1025
Username:  ada@proton.me
Password:  bridge-app-password
Security:  STARTTLS
`

func TestParseInfo(t *testing.T) {
	boxes, err := ParseInfo(infoText)
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 1 || boxes[0].Username != "ada@proton.me" || boxes[0].Password != "bridge-app-password" {
		t.Fatalf("%+v", boxes)
	}
	split := infoText + `
Configuration for ada.work@proton.me
IMAP Settings
Username:  ada.work@proton.me
Password:  second-bridge-password
SMTP Settings
Username:  ada.work@proton.me
Password:  second-bridge-password
`
	boxes, err = ParseInfo(split)
	if err != nil || len(boxes) != 2 || boxes[1].Username != "ada.work@proton.me" {
		t.Fatalf("split %+v %v", boxes, err)
	}
	if _, err := ParseInfo("No active accounts. Please add account to continue"); err != ErrNeedsLogin {
		t.Fatalf("empty account: %v", err)
	}
	if _, err := ParseInfo("Failed to create lock file; another instance is running"); err != ErrBusy {
		t.Fatalf("busy: %v", err)
	}
}

func TestPickSkipsNotificationBridge(t *testing.T) {
	all := []Candidate{
		{Name: notify.BridgeName, Image: ImageRef, State: "running"},
		{Name: "mail", Image: "docker.io/shenxn/protonmail-bridge:3.21", State: "exited"},
		{Name: AppID, Image: ImageRef, State: "running", App: AppID},
	}
	got, ok := Pick(all)
	if !ok || got.Name != AppID || got.App != AppID {
		t.Fatalf("picked %+v %v", got, ok)
	}
	if Match(all[0]) {
		t.Fatal("notification container matched the store app")
	}
	if !Match(all[1]) || !ImageMatch("index.docker.io/shenxn/protonmail-bridge@sha256:abc") {
		t.Fatal("image match failed")
	}
	if ImageMatch("sha256:abc") || ImageMatch("nginx:latest") {
		t.Fatal("unrelated image matched")
	}
}

func TestViewUsesPublishedPortsAndSavedPassword(t *testing.T) {
	creds, err := FromMailboxes([]Mailbox{{Username: "ada@proton.me", Password: "bridge-app-password"}})
	if err != nil {
		t.Fatal(err)
	}
	st := View(ViewInput{
		Found: true, ID: "abc", Name: AppID, Image: ImageRef, State: "running", Hostname: "feldbox",
		Ports: []Published{
			{HostPort: 1143, ContainerPort: 143, Proto: "tcp"},
			{HostIP: "127.0.0.1", HostPort: 1025, ContainerPort: 25, Proto: "tcp"},
		},
		Creds: &creds,
	})
	if st.Purpose != Purpose || !st.Configured || st.NeedsLogin || st.Password != "bridge-app-password" {
		t.Fatalf("status %+v", st)
	}
	if st.IMAP.Host != "feldbox" || st.IMAP.Port != 1143 || st.IMAP.Bind != "" || !st.IMAP.Insecure {
		t.Fatalf("imap %+v", st.IMAP)
	}
	if st.SMTP.Host != "127.0.0.1" || st.SMTP.Port != 1025 || st.SMTP.Bind != "127.0.0.1" {
		t.Fatalf("smtp %+v", st.SMTP)
	}
	if !strings.Contains(st.Plan, "paid plan") || len(st.Login.Commands) != 3 || len(st.Reauth) == 0 {
		t.Fatalf("guide missing: plan %q commands %v", st.Plan, st.Login.Commands)
	}
	if _, err := Validate("ada@proton.me", "bridge-app-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate("ada proton", "secret"); err == nil {
		t.Fatal("username with a space accepted")
	}
}

func TestViewWithoutContainer(t *testing.T) {
	st := View(ViewInput{})
	if st.Installed || st.Configured || !st.NeedsLogin || st.Purpose != "AI integration" {
		t.Fatalf("%+v", st)
	}
	if st.IMAP.Port != 0 || st.Notice == "" {
		t.Fatalf("missing container should explain itself: %+v", st)
	}
}
