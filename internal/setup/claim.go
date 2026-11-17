package setup

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// codeAlphabet avoids characters that are easy to misread on a console.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

const codeFile = "setup-code"

// ErrRateLimited is returned after too many wrong setup codes.
var ErrRateLimited = errors.New("too many failed setup codes")

func (s *Service) codePath() string { return filepath.Join(s.dataDir, codeFile) }

// CurrentCode reads the one-time setup code without creating one.
// It returns "" once an administrator exists or the file is gone, so the
// login banner can drop the code without minting a replacement.
func (s *Service) CurrentCode() string {
	if s.st != nil && s.st.Snapshot().Admin != nil {
		return ""
	}
	b, err := os.ReadFile(s.codePath())
	if err != nil {
		return ""
	}
	return normalizeCode(string(b))
}

// EnsureCode returns the one-time setup code, creating it if this install
// has no administrator yet. The file is root-only (the data directory is
// 0700 and the file is 0600). After the admin account exists, this returns
// "" so the code is not logged again.
func (s *Service) EnsureCode() (string, error) {
	if s.st.Snapshot().Admin != nil {
		_ = os.Remove(s.codePath())
		return "", nil
	}
	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		return "", err
	}
	path := s.codePath()
	if b, err := os.ReadFile(path); err == nil {
		if code := normalizeCode(string(b)); code != "" {
			return code, nil
		}
	}
	code, err := newCode()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(code+"\n"), 0o600); err != nil {
		return "", err
	}
	return code, nil
}

// CheckCode reports whether code matches the one on disk. It does not
// consume it; a failed account setup must be retryable.
func (s *Service) CheckCode(client, code string) error {
	if s.limited(client) {
		return ErrRateLimited
	}
	b, err := os.ReadFile(s.codePath())
	if err != nil {
		return &InputError{"The setup code is not available. It is shown on the server login screen and printed when FELDCORE starts."}
	}
	want := normalizeCode(string(b))
	got := normalizeCode(code)
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		s.fail(client)
		return &InputError{"That setup code is not right. Read it on the server login screen. It is also printed when FELDCORE starts."}
	}
	return nil
}

// ConsumeCode invalidates the setup code. Call it only after the admin
// account has been created.
func (s *Service) ConsumeCode() {
	_ = os.Remove(s.codePath())
}

func (s *Service) limited(client string) bool {
	s.lim.mu.Lock()
	defer s.lim.mu.Unlock()
	s.lim.prune(client, s.now())
	return len(s.lim.fails[client]) >= 8
}

func (s *Service) fail(client string) {
	s.lim.mu.Lock()
	defer s.lim.mu.Unlock()
	s.lim.fails[client] = append(s.lim.fails[client], s.now())
}

type codeLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func (l *codeLimiter) prune(client string, now time.Time) {
	cut := now.Add(-5 * time.Minute)
	var keep []time.Time
	for _, t := range l.fails[client] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fails, client)
		return
	}
	l.fails[client] = keep
}

func newCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = codeAlphabet[int(buf[i])%len(codeAlphabet)]
	}
	return string(buf), nil
}

func normalizeCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	if len(s) != 8 {
		return ""
	}
	for _, r := range s {
		if !strings.ContainsRune(codeAlphabet, r) {
			return ""
		}
	}
	return s
}

// FormatCode renders the code as XXXX-XXXX for the console.
func FormatCode(code string) string {
	code = normalizeCode(code)
	if len(code) != 8 {
		return code
	}
	return code[:4] + "-" + code[4:]
}
