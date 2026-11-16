package citizenid

import (
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/Herculade0/feldcore/internal/store"
)

// localProvider is the built-in directory: the administrator plus Citizen ID
// accounts. Password checks for the administrator use the existing bcrypt
// hash. Everyone else uses argon2id.
type localProvider struct {
	g *Gateway
}

func (p *localProvider) Name() string { return "local" }

func (p *localProvider) Authenticate(username, password, code string) (Identity, error) {
	return p.g.authenticateLocal(username, password, code)
}

func (g *Gateway) authenticateLocal(username, password, code string) (Identity, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		_ = verifyPassword(g.dummyPassword(), password+"pad")
		return Identity{}, ErrInvalidCredentials
	}
	snap := g.st.Snapshot()
	if snap.Admin != nil && snap.Admin.Username == username {
		if bcrypt.CompareHashAndPassword([]byte(snap.Admin.PasswordHash), []byte(password)) != nil {
			return Identity{}, ErrInvalidCredentials
		}
		id, _ := g.lookup(username)
		if err := g.totpOK(id.ID, snap.Admin.TOTP, code); err != nil {
			return Identity{}, err
		}
		return id, nil
	}
	var account *store.Account
	for i := range snap.Accounts {
		if snap.Accounts[i].Username == username {
			account = &snap.Accounts[i]
			break
		}
	}
	if account == nil {
		_ = verifyPassword(g.dummyPassword(), password)
		return Identity{}, ErrInvalidCredentials
	}
	if !verifyPassword(account.PasswordHash, password) {
		return Identity{}, ErrInvalidCredentials
	}
	if account.Disabled {
		return Identity{}, ErrDisabled
	}
	id, ok := g.lookup(username)
	if !ok {
		return Identity{}, ErrInvalidCredentials
	}
	if err := g.totpOK(id.ID, account.TOTP, code); err != nil {
		return Identity{}, err
	}
	return id, nil
}

func (g *Gateway) dummyPassword() string {
	g.dummyOnce.Do(func() {
		h, err := hashPassword(g.argon, "feldcore-timing-pad")
		if err != nil {
			g.dummy = ""
			return
		}
		g.dummy = h
	})
	return g.dummy
}
