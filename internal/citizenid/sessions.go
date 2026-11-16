package citizenid

import (
	"encoding/base64"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/Herculade0/feldcore/internal/store"
)

// sessionCap is how many Citizen ID sessions are kept. Past this, the oldest
// extra session of the busiest person is dropped. The session just written
// is never the one removed.
const sessionCap = 2000

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
	uid := sessionUser(b)
	return s.st.Update(func(st *store.State) error {
		now := time.Now()
		var created time.Time
		kept := st.CitizenSessions[:0]
		for _, se := range st.CitizenSessions {
			if se.TokenHash == token {
				created = se.CreatedAt
				continue
			}
			if se.ExpiresAt.After(now) {
				kept = append(kept, se)
			}
		}
		if created.IsZero() {
			created = now
		}
		kept = append(kept, store.CitizenSession{
			TokenHash: token, Data: data, ExpiresAt: expiry, UserID: uid, CreatedAt: created,
		})
		st.CitizenSessions = evictSessions(kept, token, sessionCap)
		return nil
	})
}

func sessionUser(b []byte) string {
	_, values, err := (scs.GobCodec{}).Decode(b)
	if err != nil || values == nil {
		return ""
	}
	uid, _ := values["uid"].(string)
	return uid
}

// evictSessions drops the oldest extra session of whoever holds the most,
// and never drops current. When everyone has one session it drops the
// oldest other session.
func evictSessions(in []store.CitizenSession, current string, cap int) []store.CitizenSession {
	rows := append([]store.CitizenSession(nil), in...)
	for len(rows) > cap {
		drop := -1
		bestCount := 1
		counts := map[string]int{}
		for _, se := range rows {
			if se.UserID != "" {
				counts[se.UserID]++
			}
		}
		for id, n := range counts {
			if n > bestCount {
				bestCount = n
			}
			_ = id
		}
		if bestCount > 1 {
			var oldest time.Time
			for i, se := range rows {
				if se.TokenHash == current || se.UserID == "" || counts[se.UserID] < bestCount {
					continue
				}
				if drop < 0 || se.CreatedAt.Before(oldest) {
					drop = i
					oldest = se.CreatedAt
				}
			}
		}
		if drop < 0 {
			var oldest time.Time
			for i, se := range rows {
				if se.TokenHash == current {
					continue
				}
				when := se.CreatedAt
				if when.IsZero() {
					when = se.ExpiresAt
				}
				if drop < 0 || when.Before(oldest) {
					drop = i
					oldest = when
				}
			}
		}
		if drop < 0 {
			break
		}
		rows = append(rows[:drop], rows[drop+1:]...)
	}
	return rows
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
