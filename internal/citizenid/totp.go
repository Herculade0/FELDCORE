package citizenid

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"net/http"
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
		return ErrSecondFactor
	}
	if g.box == nil {
		return ErrInvalidCode
	}
	secret, ok, err := g.box.Get(totpName(id))
	if err != nil || !ok || secret == "" {
		return ErrInvalidCode
	}
	valid, err := totp.ValidateCustom(code, secret, g.now(), totp.ValidateOpts{
		Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil || !valid {
		return ErrInvalidCode
	}
	return nil
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
	code = normalizeCode(code)
	valid, err := totp.ValidateCustom(code, secret, g.now(), totp.ValidateOpts{
		Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil || !valid {
		return ErrInvalidCode
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
	if ok && (p.IP != clientIP(r) || !p.Expires.After(g.now())) {
		delete(g.pending, key)
		ok = false
	}
	g.mu.Unlock()
	if !ok {
		return Identity{}, &InputError{"That sign-in expired. Enter your password again."}
	}
	ip := clientIP(r)
	if g.lim.blocked("ip:"+ip, g.now()) || g.lim.blocked("user:"+strings.ToLower(p.Username), g.now()) {
		g.record(p.Username, "sign-in failed", "locked", "denied")
		return Identity{}, ErrRateLimited
	}
	id, found := g.lookup(p.Username)
	if !found {
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
			g.lim.fail("ip:"+ip, g.now())
			g.lim.fail("user:"+strings.ToLower(p.Username), g.now())
			g.record(p.Username, "sign-in failed", "totp", "denied")
		}
		return Identity{}, err
	}
	g.lim.reset("ip:" + ip)
	g.lim.reset("user:" + strings.ToLower(p.Username))
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
	g.pending[hashKey(token)] = pendingLogin{Username: username, IP: clientIP(r), Expires: now.Add(5 * time.Minute)}
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
