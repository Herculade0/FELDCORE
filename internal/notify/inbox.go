package notify

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const inboxCap = 200

// Note is one in-app notification. It never contains a secret on purpose;
// callers scrub text before adding it.
type Note struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Severity string    `json:"severity"`
	Title    string    `json:"title"`
	Body     string    `json:"body"`
	At       time.Time `json:"at"`
	Read     bool      `json:"read"`
}

// Inbox is the bell's list, kept beside state.json so the state file stays small.
type Inbox struct {
	mu    sync.Mutex
	path  string
	items []Note
}

// OpenInbox loads the list. A missing or corrupt file starts empty.
func OpenInbox(path string) *Inbox {
	n := &Inbox{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		return n
	}
	var file struct {
		Items []Note `json:"items"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return n
	}
	if len(file.Items) > inboxCap {
		file.Items = file.Items[len(file.Items)-inboxCap:]
	}
	n.items = file.Items
	return n
}

// Add stores a note and trims the list to the newest inboxCap entries.
func (n *Inbox) Add(note Note) {
	if n == nil {
		return
	}
	if note.ID == "" {
		note.ID = noteID()
	}
	if note.At.IsZero() {
		note.At = time.Now()
	}
	n.mu.Lock()
	n.items = append(n.items, note)
	if len(n.items) > inboxCap {
		n.items = append([]Note(nil), n.items[len(n.items)-inboxCap:]...)
	}
	items := append([]Note(nil), n.items...)
	path := n.path
	n.mu.Unlock()
	if path != "" {
		_ = writeInbox(path, items)
	}
}

// List returns the unread count and the notes, newest first.
func (n *Inbox) List() (int, []Note) {
	if n == nil {
		return 0, []Note{}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	unread := 0
	out := make([]Note, len(n.items))
	for i := range n.items {
		out[len(n.items)-1-i] = n.items[i]
		if !n.items[i].Read {
			unread++
		}
	}
	if out == nil {
		out = []Note{}
	}
	return unread, out
}

// MarkRead marks every note when all is set or ids is empty, otherwise the
// named ones. "*" in ids also marks every note.
func (n *Inbox) MarkRead(all bool, ids []string) {
	if n == nil {
		return
	}
	want := map[string]bool{}
	for _, id := range ids {
		if id == "*" {
			all = true
		}
		want[id] = true
	}
	if len(ids) == 0 {
		all = true
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := range n.items {
		if all || want[n.items[i].ID] {
			n.items[i].Read = true
		}
	}
	if n.path != "" {
		_ = writeInbox(n.path, append([]Note(nil), n.items...))
	}
}

func writeInbox(path string, items []Note) error {
	raw, err := json.Marshal(struct {
		Items []Note `json:"items"`
	}{Items: items})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".inbox-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func noteID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "note"
	}
	return hex.EncodeToString(b)
}
