package citizenid

import (
	"strings"

	"github.com/Herculade0/feldcore/internal/store"
)

// Effective is the access level for a container, combining the Citizen ID
// app grant and the publish access. When neither is set the route stays
// public, which is how an open web route has always behaved. When a record
// exists but its level is empty, the result is admin. When both are set,
// the stricter one wins.
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
			return LevelGroups, unionGroups(grantGroups, accessGroups)
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

func unionGroups(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{a, b} {
		for _, g := range list {
			g = strings.TrimSpace(g)
			if g == "" || seen[g] {
				continue
			}
			seen[g] = true
			out = append(out, g)
		}
	}
	return out
}
