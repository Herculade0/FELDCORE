package citizenid

import (
	"strings"

	"github.com/Herculade0/feldcore/internal/store"
)

// accessRule is one side of an access decision. set is false when that side
// has no rule, which is different from an explicit public rule: a missing
// publish toggle must not wipe a Citizen ID grant, and the other way around.
type accessRule struct {
	set    bool
	level  string
	groups []string
}

// Effective is the access level for a container, combining the Citizen ID
// app grant and the publish access. When neither is set the route stays
// public, which is how an open web route has always behaved. When a record
// exists but its level is empty, the result is admin. When both are set,
// the stricter one wins. Two group lists are intersected.
func Effective(snap store.State, container string) (level string, groups []string, set bool) {
	hasGrant, grantLevel, grantGroups := findGrant(snap, container)
	hasPub, access, pubGroups := findPublish(snap, container)
	if !hasGrant && !hasPub {
		return LevelPublic, nil, false
	}
	level, groups = combine(hasGrant, grantLevel, grantGroups, hasPub, access, pubGroups)
	return level, groups, true
}

// Combine merges one publish app's access with any app grant for the same
// container. The publish app counts as set even when its access is empty.
func Combine(snap store.State, container, access string, groups []string) (string, []string) {
	hasGrant, grantLevel, grantGroups := findGrant(snap, container)
	return combine(hasGrant, grantLevel, grantGroups, true, access, groups)
}

func findGrant(snap store.State, container string) (bool, string, []string) {
	for _, g := range snap.AppGrants {
		if g.Container == container {
			return true, g.Level, append([]string(nil), g.GroupIDs...)
		}
	}
	return false, "", nil
}

func findPublish(snap store.State, container string) (bool, string, []string) {
	for _, app := range snap.Publish.Apps {
		if app.Enabled && app.Container == container {
			return true, app.Access, append([]string(nil), app.Groups...)
		}
	}
	return false, "", nil
}

func combine(hasGrant bool, grantLevel string, grantGroups []string, hasAccess bool, access string, accessGroups []string) (string, []string) {
	gLevel, gOK := knownGrant(grantLevel)
	if !hasGrant {
		gOK = false
	}
	aLevel, aOK := knownAccess(access)
	if !hasAccess {
		aOK = false
	}
	if !gOK && !aOK {
		return LevelAdmin, nil
	}
	gRank, _ := rank(gLevel)
	aRank, _ := rank(aLevel)
	switch {
	case gOK && aOK && gRank == aRank:
		if gLevel == LevelGroups {
			return LevelGroups, intersectIDs(grantGroups, accessGroups)
		}
		return gLevel, nil
	case gOK && (!aOK || gRank > aRank):
		if gLevel == LevelGroups {
			return LevelGroups, append([]string(nil), grantGroups...)
		}
		return gLevel, nil
	default:
		if aLevel == LevelGroups {
			return LevelGroups, append([]string(nil), accessGroups...)
		}
		return aLevel, nil
	}
}

func knownGrant(level string) (string, bool) {
	switch strings.TrimSpace(level) {
	case LevelPublic, LevelUsers, LevelGroups, LevelAdmin:
		return strings.TrimSpace(level), true
	case "":
		return "", false
	default:
		return LevelAdmin, true
	}
}

func knownAccess(access string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(access)) {
	case "public":
		return LevelPublic, true
	case "signed-in", "signedin", "authenticated":
		return LevelUsers, true
	case "groups", "group":
		return LevelGroups, true
	case "admins", "admin":
		return LevelAdmin, true
	case "":
		return "", false
	default:
		return LevelAdmin, true
	}
}

func rank(level string) (int, bool) {
	switch level {
	case LevelPublic:
		return 0, true
	case LevelUsers:
		return 1, true
	case LevelGroups:
		return 2, true
	case LevelAdmin:
		return 3, true
	default:
		return 3, false
	}
}

func intersectIDs(a, b []string) []string {
	have := map[string]bool{}
	for _, id := range a {
		id = strings.TrimSpace(id)
		if id != "" {
			have[id] = true
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range b {
		id = strings.TrimSpace(id)
		if id == "" || !have[id] || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func (g *Gateway) grantRule(container string) accessRule {
	if g == nil || g.st == nil {
		return accessRule{}
	}
	for _, gr := range g.st.Snapshot().AppGrants {
		if gr.Container != container {
			continue
		}
		rule := accessRule{set: true, level: gr.Level}
		if level, ok := knownGrant(gr.Level); ok && level == LevelGroups {
			rule.groups = g.ResolveGroups(gr.GroupIDs)
		}
		return rule
	}
	return accessRule{}
}

func (g *Gateway) publishRule(container, host string) accessRule {
	if g == nil || g.st == nil {
		return accessRule{}
	}
	var fallback accessRule
	found := false
	for _, app := range g.st.Snapshot().Publish.Apps {
		if !app.Enabled || app.Container != container {
			continue
		}
		rule := publishAccessRule(g, app.Access, app.Groups)
		if host != "" && strings.EqualFold(app.Hostname, host) {
			return rule
		}
		if !found {
			fallback = rule
			found = true
		}
	}
	if !found {
		return accessRule{}
	}
	return fallback
}

func publishAccessRule(g *Gateway, access string, groups []string) accessRule {
	rule := accessRule{set: true, level: access}
	if level, ok := knownAccess(access); ok && level == LevelGroups {
		rule.groups = g.ResolveGroups(groups)
	}
	return rule
}

// Effective is the rule a web route and the directory both show: the stricter
// of the Citizen ID grant and the publish toggle for this hostname.
func (g *Gateway) Effective(container, host string) (string, []string) {
	grant := g.grantRule(container)
	pub := g.publishRule(container, host)
	if !grant.set && !pub.set {
		return LevelPublic, []string{}
	}
	level, groups := combine(grant.set, grant.level, grant.groups, pub.set, pub.level, pub.groups)
	if groups == nil {
		groups = []string{}
	}
	return level, groups
}

// EffectivePublish combines a publish record the caller already has with the
// stored Citizen ID grant. The publish side is always set, even when it is public.
// Group names are resolved to ids before the lists are intersected.
func (g *Gateway) EffectivePublish(container, level string, groups []string) (string, []string) {
	grant := g.grantRule(container)
	pub := publishAccessRule(g, level, groups)
	out, ids := combine(grant.set, grant.level, grant.groups, true, pub.level, pub.groups)
	if ids == nil {
		ids = []string{}
	}
	return out, ids
}
