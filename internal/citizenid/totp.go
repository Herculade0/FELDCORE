package citizenid

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/Herculade0/feldcore/internal/store"
)

// TOTPSetup is what an authenticator app needs. The secret is shown once
// here; it is stored only in the secrets box.
type TOTPSetup struct {
	Secret string `json:"secret"`
	URL    string `json:"url"`
	QR     string `json:"qr,omitempty"`
}

func totpName(id string) string    { return "totp-" + id }
func totpPending(id string) string { return "totp-p-" + id }
func totpUsed(id string) string    { return "totp-used-" + id }

// secondFactorError is a missing authenticator code. It does not count as a
// failed password. errors.Is matches ErrSecondFactor.
type secondFactorError struct{}

func (secondFactorError) Error() string             { return ErrSecondFactor.Error() }
func (secondFactorError) Is(err error) bool         { return err == ErrSecondFactor }
func (secondFactorError) SecondFactorPending() bool { return true }

func normalizeCode(code string) string {
	code = strings.TrimSpace(code)
	code = strings.ReplaceAll(code, " ", "")
	return strings.ReplaceAll(code, "-", "")
}

func (g *Gateway) totpOK(id string, enrolled bool, code string) error {
	if !enrolled {
		return nil
	}
	code = normalizeCode(code)
	if code == "" {
		return secondFactorError{}
	}
	if g.box == nil {
		return ErrInvalidCode
	}
	secret, ok, err := g.box.Get(totpName(id))
	if err != nil || !ok || secret == "" {
		return ErrInvalidCode
	}
	return g.acceptStep(id, secret, code)
}

// acceptStep accepts code for secret when it matches the current window
// (one step either side) and has not been used before. The matched step is
// stored so the same code cannot be replayed.
func (g *Gateway) acceptStep(id, secret, code string) error {
	code = normalizeCode(code)
	now := g.now()
	base := now.Unix() / 30
	var matched int64
	found := false
	for _, delta := range []int64{0, -1, 1} {
		step := base + delta
		at := time.Unix(step*30+15, 0).UTC()
		valid, err := totp.ValidateCustom(code, secret, at, totp.ValidateOpts{
			Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && valid {
			matched = step
			found = true
			break
		}
	}
	if !found {
		return ErrInvalidCode
	}
	g.stepMu.Lock()
	defer g.stepMu.Unlock()
	last := int64(0)
	if g.box != nil {
		if raw, ok, err := g.box.Get(totpUsed(id)); err == nil && ok && raw != "" {
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
				last = n
			}
		}
	}
	if matched <= last {
		return ErrInvalidCode
	}
	if g.box == nil {
		return ErrInvalidCode
	}
	if err := g.box.Put(totpUsed(id), strconv.FormatInt(matched, 10)); err != nil {
		return err
	}
	return nil
}

// AdminFactor is the authenticator check for the original administrator
// sign-in. It allows the password through when no authenticator is enrolled.
func (g *Gateway) AdminFactor(username, code string) error {
	snap := g.st.Snapshot()
	if snap.Admin == nil || snap.Admin.Username != username || !snap.Admin.TOTP {
		return nil
	}
	return g.totpOK("admin", true, code)
}

// FactorOn reports whether id already has an authenticator.
func (g *Gateway) FactorOn(id Identity) bool {
	snap := g.st.Snapshot()
	if id.ID == "admin" && snap.Admin != nil {
		return snap.Admin.TOTP
	}
	for _, a := range snap.Accounts {
		if a.ID == id.ID {
			return a.TOTP
		}
	}
	return false
}

// ConfirmCaller checks the caller's password and, when an authenticator is
// enrolled, the current code. A signed-in session on its own is not enough
// to change or remove a factor.
func (g *Gateway) ConfirmCaller(r *http.Request, id Identity, password, code string) error {
	_, err := g.authenticate(r, id.Username, password, code)
	return err
}

// BeginTOTP starts enrolment. The previous authenticator keeps working until
// the code from the new one is confirmed.
func (g *Gateway) BeginTOTP(id Identity) (TOTPSetup, error) {
	if g.box == nil {
		return TOTPSetup{}, &InputError{"The secrets store is not available."}
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer: "FELDCORE", AccountName: id.Username,
		Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return TOTPSetup{}, err
	}
	if err := g.box.Put(totpPending(id.ID), key.Secret()); err != nil {
		return TOTPSetup{}, err
	}
	setup := TOTPSetup{Secret: key.Secret(), URL: key.URL()}
	if img, err := key.Image(240, 240); err == nil {
		var buf bytes.Buffer
		if png.Encode(&buf, img) == nil {
			setup.QR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		}
	}
	return setup, nil
}

// ConfirmTOTP turns the pending authenticator on.
func (g *Gateway) ConfirmTOTP(id Identity, code string) error {
	if g.box == nil {
		return &InputError{"The secrets store is not available."}
	}
	secret, ok, err := g.box.Get(totpPending(id.ID))
	if err != nil {
		return err
	}
	if !ok || secret == "" {
		return &InputError{"Set up an authenticator first."}
	}
	if err := g.acceptStep(id.ID, secret, code); err != nil {
		return err
	}
	if err := g.box.Put(totpName(id.ID), secret); err != nil {
		return err
	}
	_ = g.box.Put(totpPending(id.ID), "")
	return g.st.Update(func(s *store.State) error {
		if id.ID == "admin" {
			if s.Admin == nil {
				return ErrNotFound
			}
			s.Admin.TOTP = true
			return nil
		}
		for i := range s.Accounts {
			if s.Accounts[i].ID == id.ID {
				s.Accounts[i].TOTP = true
				return nil
			}
		}
		return ErrNotFound
	})
}

// ResetTOTP removes the authenticator. The person can sign in with a password
// until they enrol again. id is "admin" or an account id.
func (g *Gateway) ResetTOTP(id string) error {
	if g.box != nil {
		_ = g.box.Put(totpName(id), "")
		_ = g.box.Put(totpPending(id), "")
	}
	return g.st.Update(func(s *store.State) error {
		if id == "admin" {
			if s.Admin == nil {
				return ErrNotFound
			}
			s.Admin.TOTP = false
			return nil
		}
		for i := range s.Accounts {
			if s.Accounts[i].ID == id {
				s.Accounts[i].TOTP = false
				return nil
			}
		}
		return ErrNotFound
	})
}

// FinishSecondFactor completes a sign-in that already passed the password
// check. The password is not kept; only a short-lived pending cookie is.
func (g *Gateway) FinishSecondFactor(w http.ResponseWriter, r *http.Request, code string) (Identity, error) {
	c, err := r.Cookie(pendingCookie)
	if err != nil || c.Value == "" {
		return Identity{}, &InputError{"That sign-in expired. Enter your password again."}
	}
	key := hashKey(c.Value)
	g.mu.Lock()
	p, ok := g.pending[key]
	if ok && (p.IP != g.clientIP(r) || !p.Expires.After(g.now())) {
		delete(g.pending, key)
		ok = false
	}
	g.mu.Unlock()
	if !ok {
		return Identity{}, &InputError{"That sign-in expired. Enter your password again."}
	}
	ip := g.clientIP(r)
	userKey := "user:" + strings.ToLower(p.Username)
	done, allowed := g.lim.begin(g.now(), "ip:"+ip, userKey)
	if !allowed {
		g.record(p.Username, "sign-in failed", "locked", "denied")
		return Identity{}, ErrRateLimited
	}
	id, found := g.lookup(p.Username)
	if !found {
		done(true)
		return Identity{}, ErrInvalidCredentials
	}
	enrolled := false
	snap := g.st.Snapshot()
	if id.Admin && snap.Admin != nil {
		enrolled = snap.Admin.TOTP
	} else {
		for _, a := range snap.Accounts {
			if a.ID == id.ID {
				enrolled = a.TOTP
			}
		}
	}
	if err := g.totpOK(id.ID, enrolled, code); err != nil {
		if !errorsIsSecond(err) {
			done(true)
			g.record(p.Username, "sign-in failed", "totp", "denied")
		} else {
			done(false)
		}
		return Identity{}, err
	}
	done(false)
	g.lim.reset(userKey)
	g.mu.Lock()
	delete(g.pending, key)
	g.mu.Unlock()
	clearCookie(w, pendingCookie, r)
	if err := g.startSession(w, r, id, "password+totp"); err != nil {
		return Identity{}, err
	}
	g.record(id.Username, "sign-in", "password+totp", "ok")
	return id, nil
}

func errorsIsSecond(err error) bool {
	return err == ErrSecondFactor
}

func (g *Gateway) issuePending(w http.ResponseWriter, r *http.Request, username string) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	g.mu.Lock()
	now := g.now()
	for k, p := range g.pending {
		if !p.Expires.After(now) {
			delete(g.pending, k)
		}
	}
	if len(g.pending) > 1024 {
		g.pending = map[string]pendingLogin{}
	}
	g.pending[hashKey(token)] = pendingLogin{Username: username, IP: g.clientIP(r), Expires: now.Add(5 * time.Minute)}
	g.mu.Unlock()
	httpSet(w, r, pendingCookie, token, "/citizen", 300)
	return nil
}

func (g *Gateway) totpEnrolled(id Identity) bool {
	snap := g.st.Snapshot()
	if id.Admin {
		return snap.Admin != nil && snap.Admin.TOTP
	}
	for _, a := range snap.Accounts {
		if a.ID == id.ID {
			return a.TOTP
		}
	}
	return false
}
