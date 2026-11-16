package citizenid

import (
	"encoding/base64"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// sessionStore is the scs backend. Tokens arrive already hashed
// (SessionManager.HashTokenInStore), so the state file never holds a
// usable cookie value. There is no separate database.
type sessionStore struct {
	st *store.Store
}

func (s *sessionStore) Find(token string) ([]byte, bool, error) {
	if token == "" {
		return nil, false, nil
	}
	now := time.Now()
	for _, se := range s.st.Snapshot().CitizenSessions {
		if se.TokenHash != token {
			continue
		}
		if !se.ExpiresAt.After(now) {
			return nil, false, nil
		}
		raw, err := base64.RawStdEncoding.DecodeString(se.Data)
		if err != nil {
			return nil, false, nil
		}
		return raw, true, nil
	}
	return nil, false, nil
}

func (s *sessionStore) Commit(token string, b []byte, expiry time.Time) error {
	data := base64.RawStdEncoding.EncodeToString(b)
	return s.st.Update(func(st *store.State) error {
		now := time.Now()
		kept := st.CitizenSessions[:0]
		for _, se := range st.CitizenSessions {
			if se.TokenHash != token && se.ExpiresAt.After(now) {
				kept = append(kept, se)
			}
		}
		kept = append(kept, store.CitizenSession{TokenHash: token, Data: data, ExpiresAt: expiry})
		if len(kept) > 2000 {
			kept = kept[len(kept)-2000:]
		}
		st.CitizenSessions = kept
		return nil
	})
}

func (s *sessionStore) Delete(token string) error {
	if token == "" {
		return nil
	}
	return s.st.Update(func(st *store.State) error {
		kept := st.CitizenSessions[:0]
		for _, se := range st.CitizenSessions {
			if se.TokenHash != token {
				kept = append(kept, se)
			}
		}
		st.CitizenSessions = kept
		return nil
	})
}
