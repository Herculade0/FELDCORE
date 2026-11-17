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
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Herculade0/feldcore/internal/store"
)

var (
	ErrTOTPRequired = errors.New("authenticator code required")
	ErrTOTPInvalid  = errors.New("incorrect authenticator code")
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

// SecondFactor runs after the administrator password matches. Citizen ID
// installs this so the authenticator it shares with the setup wizard is
// checked here. A nil hook falls back to that same authenticator when the
// wizard turned it on, and stays password-only until then.
type SecondFactor func(username, code string) error

// InputError is a validation failure whose message is safe to show the user.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// Service owns accounts and sessions.
type Service struct {
	st      *store.Store
	cost    int
	lim     *limiter
	now     func() time.Time
	secrets SecretBox

	dummyOnce sync.Once
	factor    SecondFactor
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

// SetSecondFactor installs the authenticator check used by Login.
// The hook is nil until Citizen ID is wired in.
func (a *Service) SetSecondFactor(fn SecondFactor) {
	a.factor = fn
}

// Login checks credentials and, on success, returns a new session token.
// clientKey identifies the caller for rate limiting (normally its IP).
func (a *Service) Login(clientKey, username, password string) (token string, expires time.Time, err error) {
	return a.LoginCode(clientKey, username, password, "")
}

// LoginWithCode is the setup wizard's name for LoginCode.
func (a *Service) LoginWithCode(clientKey, username, password, code string) (token string, expires time.Time, err error) {
	return a.LoginCode(clientKey, username, password, code)
}

// LoginCode is Login with the authenticator code. An empty code is rejected
// once an authenticator is enrolled.
func (a *Service) LoginCode(clientKey, username, password, code string) (token string, expires time.Time, err error) {
	now := a.now()
	userKey := "user:" + strings.ToLower(strings.TrimSpace(username))
	ipKey := "ip:" + clientKey
	keys := []string{ipKey}
	if strings.TrimSpace(username) != "" {
		keys = append(keys, userKey)
	}
	done, allowed := a.lim.begin(now, keys...)
	if !allowed {
		return "", time.Time{}, ErrRateLimited
	}

	admin := a.st.Snapshot().Admin
	// Always run one bcrypt comparison so a wrong username takes as long as
	// a wrong password. bcrypt refuses input past 72 bytes immediately, which
	// would skip that work, so an over-long password is compared as a pad.
	hash := a.dummyHash()
	userOK := false
	totpOn := false
	if admin != nil && subtle.ConstantTimeCompare([]byte(admin.Username), []byte(username)) == 1 {
		hash = []byte(admin.PasswordHash)
		userOK = true
		totpOn = admin.TOTP
	}
	tooLong := len(password) > maxPassword
	compare := password
	if tooLong {
		compare = "feldcore-timing-pad"
	}
	passOK := bcrypt.CompareHashAndPassword(hash, []byte(compare)) == nil && !tooLong
	if !userOK || !passOK {
		done(true)
		return "", time.Time{}, ErrInvalidCredentials
	}
	if a.factor != nil {
		if ferr := a.factor(username, code); ferr != nil {
			if a.RedeemRecovery(username, code) {
				done(false)
				a.lim.reset(userKey)
				return a.issue(username)
			}
			if !factorPending(ferr) {
				done(true)
			} else {
				done(false)
			}
			return "", time.Time{}, ferr
		}
	} else if totpOn {
		if strings.TrimSpace(code) == "" {
			done(false)
			return "", time.Time{}, ErrTOTPRequired
		}
		if !a.codeMatches(code) {
			done(true)
			return "", time.Time{}, ErrTOTPInvalid
		}
	}
	done(false)
	// A success clears the username bucket only. The address keeps its
	// failures, so one good password cannot reset a shared IP.
	a.lim.reset(userKey)
	return a.issue(username)
}

// IssueSession starts an administrator session after Citizen ID has already
// checked the password and the authenticator. It does not check them again.
func (a *Service) IssueSession(username string) (string, time.Time, error) {
	admin := a.st.Snapshot().Admin
	if admin == nil || subtle.ConstantTimeCompare([]byte(admin.Username), []byte(username)) != 1 {
		return "", time.Time{}, ErrInvalidCredentials
	}
	return a.issue(username)
}

func factorPending(err error) bool {
	var p interface{ SecondFactorPending() bool }
	return errors.As(err, &p) && p.SecondFactorPending()
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

// AllowLANURLs reports whether a share may fetch a URL on a private network.
// Loopback, link-local and metadata addresses stay blocked either way.
func (a *Service) AllowLANURLs() bool {
	return a.st.Snapshot().Prefs.AllowLANURLs
}

// SetAllowLANURLs stores the file browser's LAN download switch.
func (a *Service) SetAllowLANURLs(on bool) error {
	return a.st.Update(func(s *store.State) error {
		s.Prefs.AllowLANURLs = on
		return nil
	})
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

// CheckPassword reports whether password is the administrator's current one.
// client is the caller address. Every check, right or wrong, counts, so the
// call cannot be used as an unlimited password oracle.
func (a *Service) CheckPassword(client, password string) error {
	if strings.TrimSpace(client) == "" {
		client = "local"
	}
	done, allowed := a.lim.begin(a.now(), "check:"+client)
	if !allowed {
		return ErrRateLimited
	}
	admin := a.st.Snapshot().Admin
	if admin == nil {
		done(true)
		return ErrSetupDone
	}
	tooLong := len(password) > maxPassword
	compare := password
	if tooLong {
		compare = "feldcore-timing-pad"
	}
	match := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(compare)) == nil && !tooLong
	done(true)
	if !match {
		return ErrInvalidCredentials
	}
	return nil
}

// ChangePassword replaces the administrator password after checking the current one.
// keep is the session token that should stay signed in. Every other live
// administrator session is revoked in the same update.
func (a *Service) ChangePassword(current, next, keep string) error {
	admin := a.st.Snapshot().Admin
	if admin == nil {
		return ErrSetupDone
	}
	if err := validate(admin.Username, next); err != nil {
		return err
	}
	if len(current) > maxPassword || bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(current)) != nil {
		return ErrInvalidCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), a.cost)
	if err != nil {
		return err
	}
	keepHash := ""
	if keep != "" {
		keepHash = hashToken(keep)
	}
	now := a.now()
	return a.st.Update(func(s *store.State) error {
		if s.Admin == nil {
			return ErrSetupDone
		}
		s.Admin.PasswordHash = string(hash)
		// Passkeys stay: this person still has them, and proved the current
		// password. An administrator reset removes passkeys instead.
		// keep is the API session that stays. Every other one is removed.
		s.Admin.SessionVersion++
		kept := s.Sessions[:0]
		for _, se := range s.Sessions {
			if !se.ExpiresAt.After(now) {
				continue
			}
			if keepHash != "" && subtle.ConstantTimeCompare([]byte(se.TokenHash), []byte(keepHash)) == 1 {
				kept = append(kept, se)
			}
		}
		s.Sessions = kept
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

// limiter is a fixed-window count of sign-in attempts. begin reserves a slot
// before the password check, so two requests cannot both pass the check
// before either of them is recorded.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*bucket
}

type bucket struct {
	n       int
	pending int
	start   time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string]*bucket{}}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

// begin reserves one attempt against every key. The reservation counts
// toward the lockout, so two requests cannot both pass the check before
// either of them is recorded. done(true) records a failure. done(false)
// releases the reservation and leaves the counters as they were.
func (l *limiter) begin(now time.Time, keys ...string) (func(failed bool), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 4096 {
		for k, b := range l.hits {
			if b.pending == 0 && now.Sub(b.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	for _, k := range keys {
		b := l.hits[k]
		if b == nil || now.Sub(b.start) > l.window {
			continue
		}
		if b.n+b.pending >= l.max {
			return nil, false
		}
	}
	for _, k := range keys {
		b := l.hits[k]
		if b == nil || now.Sub(b.start) > l.window {
			l.hits[k] = &bucket{pending: 1, start: now}
			continue
		}
		b.pending++
	}
	return func(failed bool) {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, k := range keys {
			b := l.hits[k]
			if b == nil {
				continue
			}
			if b.pending > 0 {
				b.pending--
			}
			if !failed {
				continue
			}
			if now.Sub(b.start) > l.window {
				b.n = 1
				b.start = now
				continue
			}
			b.n++
		}
	}, true
}
