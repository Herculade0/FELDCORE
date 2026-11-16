package citizenid

import (
	"regexp"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

const (
	maxUsers    = 100
	maxGroups   = 40
	maxPasskeys = 8
)

var (
	usernameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)
	groupNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,31}$`)
	containerRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
)

// UserInfo is a person, safe to show. The administrator is included with Admin set.
type UserInfo struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName,omitempty"`
	Disabled    bool     `json:"disabled"`
	GroupIDs    []string `json:"groupIds"`
	TOTP        bool     `json:"totp"`
	Passkeys    int      `json:"passkeys"`
	Admin       bool     `json:"admin,omitempty"`
}

// GroupInfo is a group.
type GroupInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AppInfo is a published app and who may open it.
type AppInfo struct {
	Container string   `json:"container"`
	Host      string   `json:"host"`
	URL       string   `json:"url"`
	Level     string   `json:"level"`
	GroupIDs  []string `json:"groupIds"`
}

// Directory is the Citizen ID page.
type Directory struct {
	Admin  UserInfo          `json:"admin"`
	Users  []UserInfo        `json:"users"`
	Groups []GroupInfo       `json:"groups"`
	Apps   []AppInfo         `json:"apps"`
	Domain string            `json:"domain"`
	Brand  store.SignInBrand `json:"brand"`
}

// LauncherApp is one app a signed-in person is allowed to open.
type LauncherApp struct {
	Container string `json:"container"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	URL       string `json:"url"`
	Level     string `json:"level"`
}

// Profile is the signed-in person's own sign-in methods.
type Profile struct {
	Username    string        `json:"username"`
	DisplayName string        `json:"displayName,omitempty"`
	Admin       bool          `json:"admin"`
	TOTP        bool          `json:"totp"`
	Passkeys    []PasskeyInfo `json:"passkeys"`
}

// PasskeyInfo is a passkey without its public key.
type PasskeyInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

func (g *Gateway) Directory() Directory {
	snap := g.st.Snapshot()
	brand := snap.CitizenBrand
	if brand.Links == nil {
		brand.Links = []store.BrandLink{}
	}
	out := Directory{
		Users: []UserInfo{}, Groups: []GroupInfo{}, Apps: []AppInfo{},
		Domain: g.parentDomain(), Brand: brand,
	}
	if snap.Admin != nil {
		out.Admin = g.userInfoAdmin(snap)
	}
	for _, a := range snap.Accounts {
		out.Users = append(out.Users, g.userInfo(snap, a))
	}
	for _, gr := range snap.Groups {
		out.Groups = append(out.Groups, GroupInfo{ID: gr.ID, Name: gr.Name, Description: gr.Description})
	}
	grants := map[string]store.AppGrant{}
	for _, gr := range snap.AppGrants {
		grants[gr.Container] = gr
	}
	for _, rt := range snap.Web.Routes {
		if !rt.Enabled || rt.Hostname == "" {
			continue
		}
		info := AppInfo{
			Container: rt.Container, Host: rt.Hostname, URL: routeURL(rt),
			Level: LevelPublic, GroupIDs: []string{},
		}
		if gr, ok := grants[rt.Container]; ok && gr.Level != "" {
			info.Level = gr.Level
			info.GroupIDs = append([]string{}, gr.GroupIDs...)
			if info.GroupIDs == nil {
				info.GroupIDs = []string{}
			}
		}
		out.Apps = append(out.Apps, info)
	}
	return out
}

func (g *Gateway) Profile(id Identity) Profile {
	return Profile{
		Username: id.Username, DisplayName: id.DisplayName, Admin: id.Admin,
		TOTP: g.totpEnrolled(id), Passkeys: g.passkeyInfos(id.ID),
	}
}

// Launcher lists the published apps this person may open.
func (g *Gateway) Launcher(id Identity) []LauncherApp {
	snap := g.st.Snapshot()
	grants := map[string]store.AppGrant{}
	for _, gr := range snap.AppGrants {
		grants[gr.Container] = gr
	}
	var out []LauncherApp
	for _, rt := range snap.Web.Routes {
		if !rt.Enabled || rt.Hostname == "" {
			continue
		}
		app := AppAccess{ID: rt.Container, Name: rt.Hostname, Host: rt.Hostname, Level: LevelPublic}
		if gr, ok := grants[rt.Container]; ok && gr.Level != "" {
			app.Level = gr.Level
			app.GroupIDs = gr.GroupIDs
		}
		if !id.Admin && !allowed(id, app) {
			continue
		}
		name := rt.Container
		if i := strings.IndexByte(rt.Hostname, '.'); i > 0 {
			name = rt.Hostname[:i]
		}
		out = append(out, LauncherApp{
			Container: rt.Container, Name: name, Host: rt.Hostname,
			URL: routeURL(rt), Level: app.Level,
		})
	}
	if out == nil {
		out = []LauncherApp{}
	}
	return out
}

func routeURL(rt store.WebRoute) string {
	if rt.TLS == "off" {
		return "http://" + rt.Hostname
	}
	return "https://" + rt.Hostname
}

func (g *Gateway) userInfoAdmin(snap store.State) UserInfo {
	return UserInfo{
		ID: "admin", Username: snap.Admin.Username, DisplayName: snap.Admin.Username,
		GroupIDs: []string{}, TOTP: snap.Admin.TOTP, Passkeys: g.countPasskeys(snap, "admin"), Admin: true,
	}
}

func (g *Gateway) userInfo(snap store.State, a store.Account) UserInfo {
	groups := append([]string{}, a.GroupIDs...)
	if groups == nil {
		groups = []string{}
	}
	return UserInfo{
		ID: a.ID, Username: a.Username, DisplayName: a.DisplayName,
		Disabled: a.Disabled, GroupIDs: groups, TOTP: a.TOTP,
		Passkeys: g.countPasskeys(snap, a.ID),
	}
}

func (g *Gateway) countPasskeys(snap store.State, userID string) int {
	n := 0
	for _, p := range snap.Passkeys {
		if p.UserID == userID {
			n++
		}
	}
	return n
}

func (g *Gateway) passkeyInfos(userID string) []PasskeyInfo {
	var out []PasskeyInfo
	for _, p := range g.st.Snapshot().Passkeys {
		if p.UserID != userID {
			continue
		}
		name := p.Name
		if name == "" {
			name = "Passkey"
		}
		out = append(out, PasskeyInfo{ID: p.ID, Name: name, CreatedAt: p.CreatedAt})
	}
	if out == nil {
		out = []PasskeyInfo{}
	}
	return out
}

// CreateUser adds a Citizen ID account. The password is hashed with argon2id.
func (g *Gateway) CreateUser(username, display, password string, groupIDs []string) (UserInfo, error) {
	username = strings.TrimSpace(username)
	if !usernameRe.MatchString(username) {
		return UserInfo{}, &InputError{"Usernames use letters, numbers, dots, hyphens and underscores (up to 32 characters)."}
	}
	if err := checkPassword(password); err != nil {
		return UserInfo{}, err
	}
	display, err := cleanLine(display, 64, "Display names")
	if err != nil {
		return UserInfo{}, err
	}
	hash, err := hashPassword(g.argon, password)
	if err != nil {
		return UserInfo{}, err
	}
	id, err := randomID()
	if err != nil {
		return UserInfo{}, err
	}
	account := store.Account{
		ID: id, Username: username, DisplayName: display, PasswordHash: hash,
		GroupIDs: groupIDs, CreatedAt: g.now(),
	}
	err = g.st.Update(func(s *store.State) error {
		if s.Admin != nil && strings.EqualFold(s.Admin.Username, username) {
			return &InputError{"That username belongs to the administrator."}
		}
		for _, existing := range s.Accounts {
			if strings.EqualFold(existing.Username, username) {
				return &InputError{"That username is already in use."}
			}
		}
		if len(s.Accounts) >= maxUsers {
			return &InputError{"There are already 100 users."}
		}
		ids, err := checkedGroups(s, groupIDs)
		if err != nil {
			return err
		}
		account.GroupIDs = ids
		s.Accounts = append(s.Accounts, account)
		return nil
	})
	if err != nil {
		return UserInfo{}, err
	}
	return g.userInfo(g.st.Snapshot(), account), nil
}

// UserPatch is a partial update. Nil fields are left as they are.
type UserPatch struct {
	DisplayName *string
	Disabled    *bool
	GroupIDs    *[]string
}

// UpdateUser changes a Citizen ID account. The administrator cannot be disabled.
func (g *Gateway) UpdateUser(id string, patch UserPatch) (UserInfo, error) {
	if id == "admin" || id == "" {
		return UserInfo{}, &InputError{"The administrator is changed from the security settings."}
	}
	var info UserInfo
	err := g.st.Update(func(s *store.State) error {
		for i := range s.Accounts {
			if s.Accounts[i].ID != id {
				continue
			}
			if patch.DisplayName != nil {
				name, err := cleanLine(*patch.DisplayName, 64, "Display names")
				if err != nil {
					return err
				}
				s.Accounts[i].DisplayName = name
			}
			if patch.Disabled != nil && *patch.Disabled != s.Accounts[i].Disabled {
				s.Accounts[i].Disabled = *patch.Disabled
				s.Accounts[i].SessionVersion++
			}
			if patch.GroupIDs != nil {
				ids, err := checkedGroups(s, *patch.GroupIDs)
				if err != nil {
					return err
				}
				s.Accounts[i].GroupIDs = ids
			}
			info = g.userInfo(*s, s.Accounts[i])
			return nil
		}
		return ErrNotFound
	})
	if err != nil {
		return UserInfo{}, err
	}
	return info, nil
}

// SetPassword replaces a Citizen ID password and signs that person out.
func (g *Gateway) SetPassword(id, password string) error {
	if id == "admin" || id == "" {
		return &InputError{"Change the administrator password from the security settings."}
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	hash, err := hashPassword(g.argon, password)
	if err != nil {
		return err
	}
	return g.st.Update(func(s *store.State) error {
		for i := range s.Accounts {
			if s.Accounts[i].ID == id {
				s.Accounts[i].PasswordHash = hash
				s.Accounts[i].SessionVersion++
				return nil
			}
		}
		return ErrNotFound
	})
}

// DeleteUser removes an account, its authenticator and its passkeys.
func (g *Gateway) DeleteUser(id string) error {
	if id == "admin" || id == "" {
		return &InputError{"The administrator account cannot be removed."}
	}
	err := g.st.Update(func(s *store.State) error {
		kept := s.Accounts[:0]
		found := false
		for _, a := range s.Accounts {
			if a.ID == id {
				found = true
				continue
			}
			kept = append(kept, a)
		}
		if !found {
			return ErrNotFound
		}
		s.Accounts = kept
		s.Passkeys = dropPasskeys(s.Passkeys, id)
		return nil
	})
	if err != nil {
		return err
	}
	if g.box != nil {
		_ = g.box.Put(totpName(id), "")
		_ = g.box.Put(totpPending(id), "")
	}
	return nil
}

// CreateGroup adds a group.
func (g *Gateway) CreateGroup(name, description string) (GroupInfo, error) {
	name = strings.TrimSpace(name)
	if !groupNameRe.MatchString(name) {
		return GroupInfo{}, &InputError{"Group names use letters, numbers, spaces, dots, hyphens and underscores (up to 32 characters)."}
	}
	description, err := cleanLine(description, 200, "Descriptions")
	if err != nil {
		return GroupInfo{}, err
	}
	id, err := randomID()
	if err != nil {
		return GroupInfo{}, err
	}
	gr := store.Group{ID: id, Name: name, Description: description}
	err = g.st.Update(func(s *store.State) error {
		for _, existing := range s.Groups {
			if strings.EqualFold(existing.Name, name) {
				return &InputError{"A group with that name already exists."}
			}
		}
		if len(s.Groups) >= maxGroups {
			return &InputError{"There are already 40 groups."}
		}
		s.Groups = append(s.Groups, gr)
		return nil
	})
	if err != nil {
		return GroupInfo{}, err
	}
	return GroupInfo{ID: gr.ID, Name: gr.Name, Description: gr.Description}, nil
}

// UpdateGroup renames a group.
func (g *Gateway) UpdateGroup(id, name, description string) (GroupInfo, error) {
	name = strings.TrimSpace(name)
	if !groupNameRe.MatchString(name) {
		return GroupInfo{}, &InputError{"Group names use letters, numbers, spaces, dots, hyphens and underscores (up to 32 characters)."}
	}
	description, err := cleanLine(description, 200, "Descriptions")
	if err != nil {
		return GroupInfo{}, err
	}
	err = g.st.Update(func(s *store.State) error {
		for _, existing := range s.Groups {
			if existing.ID != id && strings.EqualFold(existing.Name, name) {
				return &InputError{"A group with that name already exists."}
			}
		}
		for i := range s.Groups {
			if s.Groups[i].ID == id {
				s.Groups[i].Name = name
				s.Groups[i].Description = description
				return nil
			}
		}
		return ErrNotFound
	})
	if err != nil {
		return GroupInfo{}, err
	}
	return GroupInfo{ID: id, Name: name, Description: description}, nil
}

// DeleteGroup removes a group from users and from app rules.
func (g *Gateway) DeleteGroup(id string) error {
	return g.st.Update(func(s *store.State) error {
		kept := s.Groups[:0]
		found := false
		for _, gr := range s.Groups {
			if gr.ID == id {
				found = true
				continue
			}
			kept = append(kept, gr)
		}
		if !found {
			return ErrNotFound
		}
		s.Groups = kept
		for i := range s.Accounts {
			s.Accounts[i].GroupIDs = removeID(s.Accounts[i].GroupIDs, id)
		}
		for i := range s.AppGrants {
			s.AppGrants[i].GroupIDs = removeID(s.AppGrants[i].GroupIDs, id)
		}
		return nil
	})
}

// SetAppAccess stores who may open a container once it is published.
func (g *Gateway) SetAppAccess(container, level string, groupIDs []string) error {
	if !containerRe.MatchString(container) {
		return &InputError{"That is not a container name."}
	}
	switch level {
	case LevelPublic, LevelUsers, LevelGroups, LevelAdmin:
	default:
		return &InputError{"Access is public, signed-in users, specific groups, or administrators only."}
	}
	return g.st.Update(func(s *store.State) error {
		ids, err := checkedGroups(s, groupIDs)
		if err != nil {
			return err
		}
		if level == LevelGroups && len(ids) == 0 {
			return &InputError{"Choose at least one group."}
		}
		if level != LevelGroups {
			ids = nil
		}
		next := s.AppGrants[:0]
		for _, gr := range s.AppGrants {
			if gr.Container != container {
				next = append(next, gr)
			}
		}
		next = append(next, store.AppGrant{Container: container, Level: level, GroupIDs: ids})
		s.AppGrants = next
		return nil
	})
}

func checkedGroups(s *store.State, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	have := map[string]bool{}
	for _, gr := range s.Groups {
		have[gr.ID] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if !have[id] {
			return nil, &InputError{"That group no longer exists."}
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func checkPassword(password string) error {
	if len(password) < 8 {
		return &InputError{"Use a password of at least 8 characters."}
	}
	if len(password) > 72 {
		return &InputError{"Use a password of at most 72 characters."}
	}
	return nil
}

func cleanLine(s string, max int, label string) (string, error) {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > max {
		return "", &InputError{label + " are too long."}
	}
	if strings.ContainsAny(s, "\r\n\t") {
		return "", &InputError{label + " are a single line."}
	}
	return s, nil
}

func removeID(ids []string, drop string) []string {
	kept := ids[:0]
	for _, id := range ids {
		if id != drop {
			kept = append(kept, id)
		}
	}
	return kept
}

func dropPasskeys(in []store.Passkey, userID string) []store.Passkey {
	kept := in[:0]
	for _, p := range in {
		if p.UserID != userID {
			kept = append(kept, p)
		}
	}
	return kept
}
