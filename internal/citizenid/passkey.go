package citizenid

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/Herculade0/feldcore/internal/store"
)

// ErrPasskey is a failed passkey ceremony. The detail stays in the log.
var ErrPasskey = errors.New("that passkey could not be verified")

type ceremony struct {
	Data    webauthn.SessionData
	UserID  string
	Kind    string
	Expires time.Time
}

type waUser struct {
	id          []byte
	name        string
	display     string
	credentials []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte                         { return u.id }
func (u *waUser) WebAuthnName() string                       { return u.name }
func (u *waUser) WebAuthnDisplayName() string                { return u.display }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// BeginPasskeyLogin starts a discoverable passkey sign-in and sets a
// short-lived ceremony cookie. The returned value is sent to the browser as JSON.
func (g *Gateway) BeginPasskeyLogin(w http.ResponseWriter, r *http.Request) (any, error) {
	wa, err := g.webAuthn(r)
	if err != nil {
		return nil, err
	}
	opts, session, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		if g.log != nil {
			g.log.Warn("passkey login could not start", "err", err)
		}
		return nil, ErrPasskey
	}
	if err := g.stash(w, r, ceremony{Data: *session, Kind: "login", Expires: g.now().Add(2 * time.Minute)}); err != nil {
		return nil, err
	}
	return opts, nil
}

// FinishPasskeyLogin completes the ceremony and sets the session cookie.
func (g *Gateway) FinishPasskeyLogin(w http.ResponseWriter, r *http.Request) (Identity, error) {
	c, err := g.take(r, "login")
	if err != nil {
		return Identity{}, err
	}
	wa, err := g.webAuthn(r)
	if err != nil {
		return Identity{}, err
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var cred *webauthn.Credential
	cred, err = wa.FinishDiscoverableLogin(g.discover, c.Data, r)
	if err != nil {
		g.lim.fail("ip:"+g.clientIP(r), g.now())
		g.recordOnce("passkey:"+g.clientIP(r), "", "sign-in failed", "passkey", "denied")
		if g.log != nil {
			g.log.Warn("passkey login failed", "err", err)
		}
		return Identity{}, ErrPasskey
	}
	if cred.Authenticator.CloneWarning {
		g.recordOnce("passkey:"+g.clientIP(r), "", "sign-in failed", "passkey", "denied")
		return Identity{}, ErrPasskey
	}
	id, err := g.identityForCredential(cred)
	if err != nil {
		return Identity{}, err
	}
	if err := g.saveCredential(id.ID, *cred); err != nil {
		return Identity{}, err
	}
	g.clearCookie(w, passkeyCookie, r)
	if err := g.startSession(w, r, id, "passkey"); err != nil {
		return Identity{}, err
	}
	g.record(id.Username, "sign-in", "passkey", "ok")
	return id, nil
}

// BeginPasskeyRegister starts enrolment for a signed-in person.
func (g *Gateway) BeginPasskeyRegister(w http.ResponseWriter, r *http.Request, id Identity) (any, error) {
	if g.countPasskeys(g.st.Snapshot(), id.ID) >= maxPasskeys {
		return nil, &InputError{"This account already has 8 passkeys."}
	}
	user, err := g.waUser(id)
	if err != nil {
		return nil, err
	}
	wa, err := g.webAuthn(r)
	if err != nil {
		return nil, err
	}
	opts, session, err := wa.BeginRegistration(&user)
	if err != nil {
		if g.log != nil {
			g.log.Warn("passkey registration could not start", "err", err)
		}
		return nil, ErrPasskey
	}
	if err := g.stash(w, r, ceremony{
		Data: *session, UserID: id.ID, Kind: "register", Expires: g.now().Add(2 * time.Minute),
	}); err != nil {
		return nil, err
	}
	return opts, nil
}

// FinishPasskeyRegister stores the new passkey.
func (g *Gateway) FinishPasskeyRegister(w http.ResponseWriter, r *http.Request, id Identity) error {
	c, err := g.take(r, "register")
	if err != nil {
		return err
	}
	if c.UserID != id.ID {
		return ErrPasskey
	}
	user, err := g.waUser(id)
	if err != nil {
		return err
	}
	wa, err := g.webAuthn(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	cred, err := wa.FinishRegistration(&user, c.Data, r)
	if err != nil {
		if g.log != nil {
			g.log.Warn("passkey registration failed", "err", err)
		}
		return ErrPasskey
	}
	if err := g.addPasskey(id, *cred); err != nil {
		return err
	}
	g.clearCookie(w, passkeyCookie, r)
	return nil
}

// DeletePasskey removes one of this person's passkeys.
func (g *Gateway) DeletePasskey(userID, passkeyID string) error {
	if userID == "" || passkeyID == "" {
		return ErrNotFound
	}
	return g.st.Update(func(s *store.State) error {
		kept := s.Passkeys[:0]
		found := false
		for _, p := range s.Passkeys {
			if p.ID == passkeyID && p.UserID == userID {
				found = true
				continue
			}
			kept = append(kept, p)
		}
		if !found {
			return ErrNotFound
		}
		s.Passkeys = kept
		return nil
	})
}

func (g *Gateway) discover(rawID, userHandle []byte) (webauthn.User, error) {
	user, err := g.waUserByHandle(userHandle)
	if err != nil {
		return nil, err
	}
	for _, c := range user.credentials {
		if bytes.Equal(c.ID, rawID) {
			return &user, nil
		}
	}
	return nil, ErrPasskey
}

func (g *Gateway) webAuthn(r *http.Request) (*webauthn.WebAuthn, error) {
	host := strings.ToLower(g.forwardedHost(r))
	if !g.passkeyHost(host) {
		return nil, &InputError{"Passkeys are not available on this address."}
	}
	rpID := host
	if parent := g.parentDomain(); parent != "" && (host == parent || strings.HasSuffix(host, "."+parent)) {
		rpID = parent
	}
	origin := "http://" + g.requestHost(r)
	if g.requestSecure(r) {
		origin = "https://" + g.requestHost(r)
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPDisplayName:               "FELDCORE",
		RPID:                        rpID,
		RPOrigins:                   []string{origin},
		RPTopOriginVerificationMode: protocol.TopOriginIgnoreVerificationMode,
		AttestationPreference:       protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, &InputError{"Passkeys are not available on this address."}
	}
	return wa, nil
}

func (g *Gateway) waUser(id Identity) (waUser, error) {
	handle, err := g.webAuthnHandle(id)
	if err != nil {
		return waUser{}, err
	}
	creds, err := g.loadCredentials(id.ID)
	if err != nil {
		return waUser{}, err
	}
	name := id.DisplayName
	if name == "" {
		name = id.Username
	}
	return waUser{id: handle, name: id.Username, display: name, credentials: creds}, nil
}

func (g *Gateway) webAuthnHandle(id Identity) ([]byte, error) {
	if id.ID != "admin" {
		return []byte(id.ID), nil
	}
	snap := g.st.Snapshot()
	if snap.Admin == nil {
		return nil, ErrNotFound
	}
	if snap.Admin.WebAuthnID != "" {
		return base64.RawURLEncoding.DecodeString(snap.Admin.WebAuthnID)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	enc := base64.RawURLEncoding.EncodeToString(raw)
	err := g.st.Update(func(s *store.State) error {
		if s.Admin == nil {
			return ErrNotFound
		}
		if s.Admin.WebAuthnID == "" {
			s.Admin.WebAuthnID = enc
		} else {
			enc = s.Admin.WebAuthnID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return base64.RawURLEncoding.DecodeString(enc)
}

func (g *Gateway) waUserByHandle(handle []byte) (waUser, error) {
	snap := g.st.Snapshot()
	if snap.Admin != nil && snap.Admin.WebAuthnID != "" {
		id, err := base64.RawURLEncoding.DecodeString(snap.Admin.WebAuthnID)
		if err == nil && len(id) == len(handle) && subtle.ConstantTimeCompare(id, handle) == 1 {
			return g.waUser(Identity{ID: "admin", Username: snap.Admin.Username, DisplayName: snap.Admin.Username, Admin: true})
		}
	}
	for _, a := range snap.Accounts {
		if a.Disabled {
			continue
		}
		if bytes.Equal([]byte(a.ID), handle) {
			return g.waUser(Identity{ID: a.ID, Username: a.Username, DisplayName: displayName(a)})
		}
	}
	return waUser{}, ErrPasskey
}

func (g *Gateway) loadCredentials(userID string) ([]webauthn.Credential, error) {
	var out []webauthn.Credential
	for _, p := range g.st.Snapshot().Passkeys {
		if p.UserID != userID {
			continue
		}
		var c webauthn.Credential
		if err := json.Unmarshal(p.Credential, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (g *Gateway) identityForCredential(cred *webauthn.Credential) (Identity, error) {
	snap := g.st.Snapshot()
	for _, p := range snap.Passkeys {
		var stored webauthn.Credential
		if json.Unmarshal(p.Credential, &stored) != nil {
			continue
		}
		if !bytes.Equal(stored.ID, cred.ID) {
			continue
		}
		if p.UserID == "admin" && snap.Admin != nil {
			id, ok := g.lookup(snap.Admin.Username)
			if !ok {
				return Identity{}, ErrPasskey
			}
			return id, nil
		}
		for _, a := range snap.Accounts {
			if a.ID == p.UserID && !a.Disabled {
				id, ok := g.lookup(a.Username)
				if !ok {
					return Identity{}, ErrPasskey
				}
				return id, nil
			}
		}
	}
	return Identity{}, ErrPasskey
}

func (g *Gateway) addPasskey(id Identity, cred webauthn.Credential) error {
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	pkID, err := randomID()
	if err != nil {
		return err
	}
	return g.st.Update(func(s *store.State) error {
		n := 0
		for _, p := range s.Passkeys {
			if p.UserID == id.ID {
				n++
			}
			var stored webauthn.Credential
			if json.Unmarshal(p.Credential, &stored) == nil && bytes.Equal(stored.ID, cred.ID) {
				return &InputError{"That passkey is already registered."}
			}
		}
		if n >= maxPasskeys {
			return &InputError{"This account already has 8 passkeys."}
		}
		s.Passkeys = append(s.Passkeys, store.Passkey{
			ID: pkID, UserID: id.ID, Name: "Passkey", CreatedAt: g.now(), Credential: raw,
		})
		return nil
	})
}

func (g *Gateway) saveCredential(userID string, cred webauthn.Credential) error {
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	return g.st.Update(func(s *store.State) error {
		for i := range s.Passkeys {
			if s.Passkeys[i].UserID != userID {
				continue
			}
			var stored webauthn.Credential
			if json.Unmarshal(s.Passkeys[i].Credential, &stored) != nil {
				continue
			}
			if bytes.Equal(stored.ID, cred.ID) {
				s.Passkeys[i].Credential = raw
				return nil
			}
		}
		return nil
	})
}

func (g *Gateway) stash(w http.ResponseWriter, r *http.Request, c ceremony) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	g.mu.Lock()
	now := g.now()
	for k, existing := range g.ceremonies {
		if !existing.Expires.After(now) {
			delete(g.ceremonies, k)
		}
	}
	// Drop the ceremony that expires soonest. Never wipe the map: another
	// person's in-flight sign-in has to survive a burst of new ones.
	for len(g.ceremonies) >= 256 {
		var oldest string
		var when time.Time
		for k, existing := range g.ceremonies {
			if oldest == "" || existing.Expires.Before(when) {
				oldest = k
				when = existing.Expires
			}
		}
		if oldest == "" {
			break
		}
		delete(g.ceremonies, oldest)
	}
	g.ceremonies[hashKey(token)] = c
	g.mu.Unlock()
	g.httpSet(w, r, passkeyCookie, token, "/", 120)
	return nil
}

func (g *Gateway) take(r *http.Request, kind string) (ceremony, error) {
	cookie, err := r.Cookie(passkeyCookie)
	if err != nil || cookie.Value == "" {
		return ceremony{}, ErrPasskey
	}
	key := hashKey(cookie.Value)
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.ceremonies[key]
	if !ok || c.Kind != kind || !c.Expires.After(g.now()) {
		delete(g.ceremonies, key)
		return ceremony{}, ErrPasskey
	}
	delete(g.ceremonies, key)
	return c, nil
}
