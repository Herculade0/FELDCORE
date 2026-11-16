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
	snap := g.st.Snapshot()
	var (
		kind       string
		bcryptHash []byte
		argonHash  string
		account    *store.Account
	)
	if snap.Admin != nil && snap.Admin.Username == username && username != "" {
		kind = "admin"
		bcryptHash = []byte(snap.Admin.PasswordHash)
	} else if username != "" {
		for i := range snap.Accounts {
			if snap.Accounts[i].Username == username {
				account = &snap.Accounts[i]
				kind = "user"
				argonHash = account.PasswordHash
				break
			}
		}
	}
	// Every attempt runs one bcrypt comparison and one argon2id comparison,
	// so an unknown name, a directory user, and the administrator take the
	// same shape of time.
	if len(bcryptHash) == 0 {
		bcryptHash = g.dummyBcrypt()
	}
	if argonHash == "" {
		argonHash = g.dummyPassword()
	}
	bcryptOK := len(bcryptHash) > 0 && bcrypt.CompareHashAndPassword(bcryptHash, []byte(password)) == nil
	argonOK := argonHash != "" && verifyPassword(argonHash, password)
	if password == "" || kind == "" || (kind == "admin" && !bcryptOK) || (kind == "user" && !argonOK) {
		return Identity{}, ErrInvalidCredentials
	}
	if kind == "user" && account != nil && account.Disabled {
		return Identity{}, ErrDisabled
	}
	id, ok := g.lookup(username)
	if !ok {
		return Identity{}, ErrInvalidCredentials
	}
	enrolled := false
	if kind == "admin" && snap.Admin != nil {
		enrolled = snap.Admin.TOTP
	} else if account != nil {
		enrolled = account.TOTP
	}
	if err := g.totpOK(id.ID, enrolled, code); err != nil {
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

func (g *Gateway) dummyBcrypt() []byte {
	g.bcryptOnce.Do(func() {
		cost := bcrypt.DefaultCost
		if g.argon.memory < 1024 {
			cost = bcrypt.MinCost
		}
		h, err := bcrypt.GenerateFromPassword([]byte("feldcore-timing-pad"), cost)
		if err != nil {
			return
		}
		g.bcryptDummy = h
	})
	return g.bcryptDummy
}
