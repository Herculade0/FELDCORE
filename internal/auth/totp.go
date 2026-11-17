package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Herculade0/feldcore/internal/store"
)

const (
	// secretTOTP and secretPending use the same names as Citizen ID so an
	// authenticator enrolled in the setup wizard is the one Citizen ID checks.
	secretTOTP     = "totp-admin"
	secretPending  = "totp-p-admin"
	secretRecovery = "admin-totp-recovery"
)

// SecretBox stores short secrets. *secrets.Box satisfies it.
type SecretBox interface {
	Put(name, value string) error
	Get(name string) (string, bool, error)
}

// UseSecrets enables authenticator storage. Without it, enrolment fails
// closed instead of writing the secret into the state file.
func (a *Service) UseSecrets(b SecretBox) { a.secrets = b }

// TOTPEnabled reports whether sign-in must include an authenticator code.
func (a *Service) TOTPEnabled() bool {
	admin := a.st.Snapshot().Admin
	return admin != nil && admin.TOTP
}

// BeginEnrolment stores a new pending authenticator secret and returns the
// base32 secret and the otpauth URL. It does not turn two-factor on.
func (a *Service) BeginEnrolment(account string) (secret, otpauth string, err error) {
	if a.secrets == nil {
		return "", "", &InputError{"Two-factor storage is not available."}
	}
	account = strings.TrimSpace(account)
	if account == "" {
		account = "admin"
	}
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	secret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	if err := a.secrets.Put(secretPending, secret); err != nil {
		return "", "", err
	}
	return secret, otpauthURL(account, secret), nil
}

// VerifyPending reports whether code matches the secret from BeginEnrolment.
// It does not enable two-factor or move the secret.
func (a *Service) VerifyPending(code string) error {
	secret, ok, err := a.pending()
	if err != nil {
		return err
	}
	if !ok {
		return &InputError{"Set up the authenticator before entering a code."}
	}
	if !totpMatch(secret, code, a.now()) {
		return &InputError{"That authenticator code is not right."}
	}
	return nil
}

// ConfirmEnrolment moves a verified pending secret into place and returns
// eight recovery codes. Two-factor stays off until EnableTOTP.
func (a *Service) ConfirmEnrolment(code string) ([]string, error) {
	if err := a.VerifyPending(code); err != nil {
		return nil, err
	}
	secret, _, err := a.pending()
	if err != nil {
		return nil, err
	}
	if err := a.secrets.Put(secretTOTP, secret); err != nil {
		return nil, err
	}
	_ = a.secrets.Put(secretPending, "")
	plain, stored, err := newRecovery()
	if err != nil {
		return nil, err
	}
	if err := a.secrets.Put(secretRecovery, stored); err != nil {
		return nil, err
	}
	return plain, nil
}

// EnableTOTP requires an authenticator code on the next sign-in.
func (a *Service) EnableTOTP() error {
	if a.secrets == nil {
		return &InputError{"Two-factor storage is not available."}
	}
	if _, ok, err := a.secrets.Get(secretTOTP); err != nil || !ok {
		return &InputError{"Set up the authenticator before turning it on."}
	}
	return a.st.Update(func(s *store.State) error {
		if s.Admin == nil {
			return ErrSetupDone
		}
		s.Admin.TOTP = true
		return nil
	})
}

// CancelEnrolment drops a pending or not-yet-required authenticator secret.
func (a *Service) CancelEnrolment() {
	if a.secrets == nil || a.TOTPEnabled() {
		return
	}
	_ = a.secrets.Put(secretPending, "")
	_ = a.secrets.Put(secretTOTP, "")
	_ = a.secrets.Put(secretRecovery, "")
}

// DisableTOTP turns two-factor off after the current password and a current
// authenticator or recovery code both match.
func (a *Service) DisableTOTP(password, code string) error {
	if !a.TOTPEnabled() {
		return nil
	}
	admin := a.st.Snapshot().Admin
	if admin == nil {
		return ErrSetupDone
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)) != nil {
		return ErrInvalidCredentials
	}
	if !a.codeMatches(code) {
		return &InputError{"That authenticator code is not right."}
	}
	if err := a.st.Update(func(s *store.State) error {
		if s.Admin == nil {
			return ErrSetupDone
		}
		s.Admin.TOTP = false
		// Same as an administrator authenticator reset: Citizen ID cookies
		// die with the version, and API sessions are removed because
		// Authenticate does not read that version.
		s.Admin.SessionVersion++
		s.Sessions = nil
		return nil
	}); err != nil {
		return err
	}
	if a.secrets != nil {
		_ = a.secrets.Put(secretPending, "")
		_ = a.secrets.Put(secretTOTP, "")
		_ = a.secrets.Put(secretRecovery, "")
	}
	return nil
}

func (a *Service) pending() (string, bool, error) {
	if a.secrets == nil {
		return "", false, &InputError{"Two-factor storage is not available."}
	}
	return a.secrets.Get(secretPending)
}

// codeMatches accepts a current authenticator code or a recovery code.
// A recovery code is removed when it matches.
func (a *Service) codeMatches(code string) bool {
	if a.secrets == nil {
		return false
	}
	secret, ok, err := a.secrets.Get(secretTOTP)
	if err != nil || !ok {
		return false
	}
	if totpMatch(secret, code, a.now()) {
		return true
	}
	return a.consumeRecovery(code)
}

// RedeemRecovery accepts one setup-wizard recovery code for the administrator
// and removes it. Citizen ID does not store these codes. A code that is not
// one of them leaves the set unchanged.
func (a *Service) RedeemRecovery(username, code string) bool {
	admin := a.st.Snapshot().Admin
	if admin == nil || !admin.TOTP {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(admin.Username), []byte(strings.TrimSpace(username))) != 1 {
		return false
	}
	return a.consumeRecovery(code)
}

func (a *Service) consumeRecovery(code string) bool {
	norm := normalizeRecovery(code)
	if norm == "" {
		return false
	}
	stored, ok, err := a.secrets.Get(secretRecovery)
	if err != nil || !ok || stored == "" {
		return false
	}
	want := sha256.Sum256([]byte(norm))
	wantHex := hex.EncodeToString(want[:])
	var keep []string
	matched := false
	for _, line := range strings.Split(stored, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(line), []byte(wantHex)) == 1 {
			matched = true
			continue
		}
		keep = append(keep, line)
	}
	if !matched {
		return false
	}
	_ = a.secrets.Put(secretRecovery, strings.Join(keep, "\n"))
	return true
}

func otpauthURL(account, secret string) string {
	u := url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + url.PathEscape("FELDCORE:"+account)}
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", "FELDCORE")
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	u.RawQuery = q.Encode()
	return u.String()
}

func totpMatch(secret, code string, now time.Time) bool {
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(raw) == 0 {
		return false
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	counter := uint64(now.Unix() / 30)
	ok := 0
	// Always check the window so a match on the first code is not faster.
	for _, delta := range []int64{-1, 0, 1} {
		c := counter
		if delta < 0 {
			if counter == 0 {
				continue
			}
			c = counter - 1
		} else if delta > 0 {
			c = counter + 1
		}
		if subtle.ConstantTimeCompare([]byte(hotp(raw, c)), []byte(code)) == 1 {
			ok = 1
		}
	}
	return ok == 1
}

func hotp(secret []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return leftPad(int(bin % 1000000))
}

func leftPad(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 6 {
		s = "0" + s
	}
	return s
}

func newRecovery() (plain []string, stored string, err error) {
	var hashes []string
	for i := 0; i < 8; i++ {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, "", err
		}
		sum := sha256.Sum256([]byte(code))
		hashes = append(hashes, hex.EncodeToString(sum[:]))
		plain = append(plain, code[:4]+"-"+code[4:])
	}
	return plain, strings.Join(hashes, "\n"), nil
}

func newRecoveryCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf), nil
}

func normalizeRecovery(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	if len(s) != 8 {
		return ""
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for _, r := range s {
		if !strings.ContainsRune(alphabet, r) {
			return ""
		}
	}
	return s
}
