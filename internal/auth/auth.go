// Package auth implements FELDCORE's login gate: a single administrator
// account, bcrypt password hashing, and server-side sessions carried in an
// HttpOnly cookie.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Herculade0/feldcore/internal/store"
)

const (
	// SessionTTL is how long a login lasts.
	SessionTTL = 7 * 24 * time.Hour

	minPassword = 8
	maxPassword = 72 // bcrypt ignores input beyond 72 bytes, so refuse it
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

var (
	ErrSetupDone          = errors.New("an administrator account already exists")
	ErrInvalidCredentials = errors.New("incorrect username or password")
	ErrRateLimited        = errors.New("too many failed attempts; wait a few minutes and try again")
)

// InputError is a validation failure whose message is safe to show the user.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// Service owns accounts and sessions.
type Service struct {
	st   *store.Store
	cost int
	lim  *limiter
	now  func() time.Time

	dummyOnce sync.Once
	dummy     []byte
}

// New returns a Service backed by st.
func New(st *store.Store) *Service {
	return &Service{st: st, cost: 12, lim: newLimiter(8, 5*time.Minute), now: time.Now}
}

// SetupRequired reports whether the administrator account is still missing.
func (a *Service) SetupRequired() bool {
	return a.st.Snapshot().Admin == nil
}

func validate(username, password string) error {
	if !usernameRe.MatchString(username) {
		return &InputError{"Usernames use letters, numbers, dots, hyphens and underscores (up to 32 characters)."}
	}
	if len(password) < minPassword {
		return &InputError{"Use a password of at least 8 characters."}
	}
	if len(password) > maxPassword {
		return &InputError{"Use a password of at most 72 characters."}
	}
	return nil
}

// Setup creates the administrator account. It only works once.
func (a *Service) Setup(username, password string) error {
	if err := validate(username, password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), a.cost)
	if err != nil {
		return err
	}
	return a.st.Update(func(s *store.State) error {
		if s.Admin != nil {
			return ErrSetupDone
		}
		s.Admin = &store.User{Username: username, PasswordHash: string(hash)}
		return nil
	})
}

// Login checks credentials and, on success, returns a new session token.
// clientKey identifies the caller for rate limiting (normally its IP).
func (a *Service) Login(clientKey, username, password string) (token string, expires time.Time, err error) {
	now := a.now()
	if a.lim.blocked(clientKey, now) {
		return "", time.Time{}, ErrRateLimited
	}

	admin := a.st.Snapshot().Admin
	// Always run one bcrypt comparison so a wrong username takes as long as
	// a wrong password.
	hash := a.dummyHash()
	userOK := false
	if admin != nil && subtle.ConstantTimeCompare([]byte(admin.Username), []byte(username)) == 1 {
		hash = []byte(admin.PasswordHash)
		userOK = true
	}
	passOK := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	if !userOK || !passOK {
		a.lim.fail(clientKey, now)
		return "", time.Time{}, ErrInvalidCredentials
	}
	a.lim.reset(clientKey)
	return a.issue(username)
}

// issue creates and stores a session for username.
func (a *Service) issue(username string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := a.now()
	expires := now.Add(a.ttl())
	err := a.st.Update(func(s *store.State) error {
		live := s.Sessions[:0]
		for _, se := range s.Sessions {
			if se.ExpiresAt.After(now) {
				live = append(live, se)
			}
		}
		s.Sessions = append(live, store.Session{
			TokenHash: hashToken(token),
			Username:  username,
			CreatedAt: now,
			ExpiresAt: expires,
		})
		return nil
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// SetupAndLogin creates the administrator and signs them in.
func (a *Service) SetupAndLogin(username, password string) (string, time.Time, error) {
	if err := a.Setup(username, password); err != nil {
		return "", time.Time{}, err
	}
	return a.issue(username)
}

// Authenticate resolves a session token to its username.
func (a *Service) Authenticate(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	want := hashToken(token)
	now := a.now()
	for _, se := range a.st.Snapshot().Sessions {
		if subtle.ConstantTimeCompare([]byte(se.TokenHash), []byte(want)) == 1 {
			if se.ExpiresAt.After(now) {
				return se.Username, true
			}
			return "", false
		}
	}
	return "", false
}

// ttl is how long a new session lasts. Settings can shorten or lengthen it.
func (a *Service) ttl() time.Duration {
	h := a.st.Snapshot().Prefs.SessionHours
	if h < 1 || h > 24*30 {
		return SessionTTL
	}
	return time.Duration(h) * time.Hour
}

// SessionHours is the timeout shown in Settings. 168 is one week, the default.
func (a *Service) SessionHours() int {
	h := a.st.Snapshot().Prefs.SessionHours
	if h < 1 || h > 24*30 {
		return int(SessionTTL / time.Hour)
	}
	return h
}

// SetSessionHours stores how long new sign-ins last. Existing sessions keep
// the expiry they were given.
func (a *Service) SetSessionHours(h int) error {
	if h < 1 || h > 24*30 {
		return &InputError{"Session timeout is between 1 and 720 hours."}
	}
	return a.st.Update(func(s *store.State) error {
		s.Prefs.SessionHours = h
		return nil
	})
}

// ChangePassword replaces the administrator password after checking the current one.
func (a *Service) ChangePassword(current, next string) error {
	admin := a.st.Snapshot().Admin
	if admin == nil {
		return ErrSetupDone
	}
	if err := validate(admin.Username, next); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(current)) != nil {
		return ErrInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), a.cost)
	if err != nil {
		return err
	}
	return a.st.Update(func(s *store.State) error {
		if s.Admin == nil {
			return ErrSetupDone
		}
		s.Admin.PasswordHash = string(hash)
		return nil
	})
}

// Logout ends the session for token, if any.
func (a *Service) Logout(token string) error {
	if token == "" {
		return nil
	}
	want := hashToken(token)
	return a.st.Update(func(s *store.State) error {
		kept := s.Sessions[:0]
		for _, se := range s.Sessions {
			if se.TokenHash != want {
				kept = append(kept, se)
			}
		}
		s.Sessions = kept
		return nil
	})
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (a *Service) dummyHash() []byte {
	a.dummyOnce.Do(func() {
		a.dummy, _ = bcrypt.GenerateFromPassword([]byte("feldcore-timing-pad"), a.cost)
	})
	return a.dummy
}

// limiter is a small fixed-window counter of failed logins per client.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*bucket
}

type bucket struct {
	n     int
	start time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string]*bucket{}}
}

func (l *limiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.hits[key]
	if !ok {
		return false
	}
	if now.Sub(b.start) > l.window {
		delete(l.hits, key)
		return false
	}
	return b.n >= l.max
}

func (l *limiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 4096 { // bound memory under a spray of source addresses
		for k, b := range l.hits {
			if now.Sub(b.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	b, ok := l.hits[key]
	if !ok || now.Sub(b.start) > l.window {
		l.hits[key] = &bucket{n: 1, start: now}
		return
	}
	b.n++
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
