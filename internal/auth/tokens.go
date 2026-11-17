package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// Scopes a token can carry. Admin includes the others. files:write includes
// files:read. backups:write includes backups:read. Shell access is not a
// scope: an agent token cannot open one, whatever it was granted.
const (
	ScopeRead         = "read"
	ScopeContainers   = "containers"
	ScopeVMs          = "vms"
	ScopeFilesRead    = "files:read"
	ScopeFilesWrite   = "files:write"
	ScopeStacks       = "stacks"
	ScopeStorage      = "storage"
	ScopeBackupsRead  = "backups:read"
	ScopeBackupsWrite = "backups:write"
	// ScopeMail reads the Proton Bridge username, app password, and the
	// IMAP and SMTP endpoints other apps use. It does not include a shell.
	ScopeMail  = "mail"
	ScopeAdmin = "admin"
)

const (
	// AuditRetention is how long a token call stays in the log.
	AuditRetention = 90 * 24 * time.Hour
	// MaxAuditEvents is the hard cap. Older calls are dropped first.
	MaxAuditEvents = 1000

	maxActiveTokens  = 25
	maxRevokedTokens = 40
	maxTokenDays     = 730
	tokenPrefixLen   = 12
)

// maxAudit is the cap Record enforces. Tests lower it.
var maxAudit = MaxAuditEvents

// ErrTokenNotFound means the id does not match a stored token.
var ErrTokenNotFound = errors.New("token not found")

var scopeOrder = []string{ScopeRead, ScopeContainers, ScopeVMs, ScopeFilesRead, ScopeFilesWrite, ScopeStacks, ScopeStorage, ScopeBackupsRead, ScopeBackupsWrite, ScopeMail, ScopeAdmin}

// Allows reports whether a token's scopes include need. Admin includes every
// real scope. files:write includes files:read, and backups:write includes
// backups:read. An unknown need is never allowed, so a shell cannot be
// smuggled in as a scope name.
func Allows(have []string, need string) bool {
	switch need {
	case ScopeRead, ScopeContainers, ScopeVMs, ScopeFilesRead, ScopeFilesWrite, ScopeStacks, ScopeStorage, ScopeBackupsRead, ScopeBackupsWrite, ScopeMail, ScopeAdmin:
	default:
		return false
	}
	for _, s := range have {
		if s == ScopeAdmin || s == need ||
			(need == ScopeFilesRead && s == ScopeFilesWrite) ||
			(need == ScopeBackupsRead && s == ScopeBackupsWrite) {
			return true
		}
	}
	return false
}

// TokenInfo is a token without its hash, safe to show in the UI.
type TokenInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	Revoked    bool       `json:"revoked"`
}

func toInfo(t store.APIToken) TokenInfo {
	scopes := append([]string(nil), t.Scopes...)
	if scopes == nil {
		scopes = []string{}
	}
	return TokenInfo{
		ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scopes: scopes,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt,
		Revoked: t.Revoked,
	}
}

// CreateToken mints a bearer token. The returned secret is the only copy of
// the plaintext; the store keeps a SHA-256 hash. expiresInDays of 0 means the
// token does not expire.
func (a *Service) CreateToken(name string, scopes []string, expiresInDays int) (secret string, info TokenInfo, err error) {
	name = strings.TrimSpace(name)
	if !tokenNameRe.MatchString(name) {
		return "", TokenInfo{}, &InputError{"Name the token with letters, numbers, spaces, dots, hyphens or underscores (up to 64 characters)."}
	}
	scopes, err = normalizeScopes(scopes)
	if err != nil {
		return "", TokenInfo{}, err
	}
	if expiresInDays < 0 || expiresInDays > maxTokenDays {
		return "", TokenInfo{}, &InputError{"Expiry is between 1 and 730 days, or leave it unset."}
	}
	secret, err = newSecret()
	if err != nil {
		return "", TokenInfo{}, err
	}
	id, err := randomID()
	if err != nil {
		return "", TokenInfo{}, err
	}
	now := a.now()
	tok := store.APIToken{
		ID:        id,
		Name:      name,
		Hash:      hashToken(secret),
		Prefix:    secret[:tokenPrefixLen],
		Scopes:    scopes,
		CreatedAt: now,
	}
	if expiresInDays > 0 {
		exp := now.Add(time.Duration(expiresInDays) * 24 * time.Hour)
		tok.ExpiresAt = &exp
	}
	err = a.st.Update(func(s *store.State) error {
		for _, existing := range s.APITokens {
			if !existing.Revoked && strings.EqualFold(existing.Name, name) {
				return &InputError{"A token with that name already exists."}
			}
		}
		active := 0
		for _, existing := range s.APITokens {
			if !existing.Revoked {
				active++
			}
		}
		if active >= maxActiveTokens {
			return &InputError{"There are already 25 agent tokens. Revoke one first."}
		}
		pruneRevoked(s)
		s.APITokens = append(s.APITokens, tok)
		return nil
	})
	if err != nil {
		return "", TokenInfo{}, err
	}
	return secret, toInfo(tok), nil
}

// Tokens lists every token, newest first. Hashes are not included.
func (a *Service) Tokens() []TokenInfo {
	in := a.st.Snapshot().APITokens
	out := make([]TokenInfo, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		out = append(out, toInfo(in[i]))
	}
	return out
}

// RevokeToken marks one token unusable. The row stays so the audit log still
// has a name to show.
func (a *Service) RevokeToken(id string) error {
	if id == "" {
		return ErrTokenNotFound
	}
	return a.st.Update(func(s *store.State) error {
		for i := range s.APITokens {
			if s.APITokens[i].ID == id {
				s.APITokens[i].Revoked = true
				return nil
			}
		}
		return ErrTokenNotFound
	})
}

// RevokeAllTokens revokes every token that is still active.
func (a *Service) RevokeAllTokens() (int, error) {
	n := 0
	err := a.st.Update(func(s *store.State) error {
		for i := range s.APITokens {
			if !s.APITokens[i].Revoked {
				s.APITokens[i].Revoked = true
				n++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// AuthenticateToken resolves a bearer secret. Revoked and expired tokens fail.
// It does not write; Record updates last-used when the call is finished.
func (a *Service) AuthenticateToken(secret string) (TokenInfo, bool) {
	if len(secret) < tokenPrefixLen || !strings.HasPrefix(secret, "fc_") {
		return TokenInfo{}, false
	}
	want := hashToken(secret)
	now := a.now()
	var match *store.APIToken
	for _, t := range a.st.Snapshot().APITokens {
		if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(want)) == 1 {
			c := t
			match = &c
		}
	}
	if match == nil || match.Revoked {
		return TokenInfo{}, false
	}
	if match.ExpiresAt != nil && !match.ExpiresAt.After(now) {
		return TokenInfo{}, false
	}
	return toInfo(*match), true
}

// Record appends one audit event and stamps the token's last-used time.
func (a *Service) Record(tokenID string, ev store.AuditEvent) error {
	if tokenID == "" && ev.TokenName == "" {
		return nil
	}
	return a.st.Update(func(s *store.State) error {
		now := a.now()
		for i := range s.APITokens {
			if s.APITokens[i].ID == tokenID {
				used := now
				s.APITokens[i].LastUsedAt = &used
				if ev.TokenName == "" {
					ev.TokenName = s.APITokens[i].Name
				}
				break
			}
		}
		if ev.ID == "" {
			id, err := randomID()
			if err != nil {
				return err
			}
			ev.ID = id
		}
		if ev.Time.IsZero() {
			ev.Time = now
		}
		ev.TokenID = tokenID
		s.Audit = append(s.Audit, ev)
		s.Audit = trimAudit(s.Audit, now)
		return nil
	})
}

// Audit returns the log, newest first.
func (a *Service) Audit() []store.AuditEvent {
	in := a.st.Snapshot().Audit
	out := make([]store.AuditEvent, len(in))
	for i := range in {
		out[len(in)-1-i] = in[i]
	}
	return out
}

func trimAudit(in []store.AuditEvent, now time.Time) []store.AuditEvent {
	cutoff := now.Add(-AuditRetention)
	live := in[:0]
	for _, ev := range in {
		if !ev.Time.Before(cutoff) {
			live = append(live, ev)
		}
	}
	// Copy so the caller's backing array is not aliased after the slice
	// expression above when live is a prefix of in.
	live = append([]store.AuditEvent(nil), live...)
	for len(live) > maxAudit {
		// Drop anonymous sign-in noise before any security event, so a
		// flood of unknown usernames cannot flush the log.
		drop := 0
		for i, ev := range live {
			if noisyAudit(ev) {
				drop = i
				break
			}
		}
		live = append(live[:drop], live[drop+1:]...)
	}
	return live
}

func noisyAudit(ev store.AuditEvent) bool {
	switch ev.Action {
	case "sign-in noise", "publish.block":
		return true
	case "sign-in failed":
		return ev.Target == "locked" || ev.Target == "passkey"
	case "access denied":
		return ev.TokenName == "Citizen ID"
	default:
		return false
	}
}

func normalizeScopes(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, &InputError{"Choose at least one scope."}
	}
	have := map[string]bool{}
	for _, s := range in {
		switch s {
		case ScopeRead, ScopeContainers, ScopeVMs, ScopeFilesRead, ScopeFilesWrite, ScopeStacks, ScopeStorage, ScopeBackupsRead, ScopeBackupsWrite, ScopeMail, ScopeAdmin:
			have[s] = true
		default:
			return nil, &InputError{"Scopes are read, containers, vms, files:read, files:write, stacks, storage, backups:read, backups:write, mail and admin."}
		}
	}
	out := make([]string, 0, len(have))
	for _, s := range scopeOrder {
		if have[s] {
			out = append(out, s)
		}
	}
	return out, nil
}

func pruneRevoked(s *store.State) {
	revoked := 0
	for _, t := range s.APITokens {
		if t.Revoked {
			revoked++
		}
	}
	if revoked <= maxRevokedTokens {
		return
	}
	drop := revoked - maxRevokedTokens
	kept := make([]store.APIToken, 0, len(s.APITokens))
	for _, t := range s.APITokens {
		if t.Revoked && drop > 0 {
			drop--
			continue
		}
		kept = append(kept, t)
	}
	s.APITokens = kept
}

func newSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "fc_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func randomID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// tokenNameRe allows a short human label such as "Claude Code".
var tokenNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)
