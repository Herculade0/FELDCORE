package citizenid

import "strings"

// accessRule is one side of an access decision. set is false when that side
// has no rule, which is different from an explicit public rule: a missing
// publish toggle must not wipe a Citizen ID grant, and the other way around.
type accessRule struct {
	set    bool
	level  string
	groups []string
}

func canonicalLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", LevelPublic:
		return LevelPublic
	case LevelUsers, "signed-in", "signedin":
		return LevelUsers
	case LevelGroups, "group":
		return LevelGroups
	case LevelAdmin, "admins":
		return LevelAdmin
	default:
		return strings.TrimSpace(level)
	}
}

func levelRank(level string) int {
	switch canonicalLevel(level) {
	case LevelPublic:
		return 0
	case LevelUsers:
		return 1
	case LevelGroups:
		return 2
	case LevelAdmin:
		return 3
	default:
		return 4
	}
}

// combineRules keeps the stricter of two rules. Two group lists are
// intersected, so a person has to be in both. An empty intersection stays
// at the groups level, which only an administrator can pass.
func combineRules(a, b accessRule) (string, []string) {
	if !a.set && !b.set {
		return LevelPublic, nil
	}
	if !a.set {
		return finishRule(b)
	}
	if !b.set {
		return finishRule(a)
	}
	ar, br := levelRank(a.level), levelRank(b.level)
	if ar > br {
		return finishRule(a)
	}
	if br > ar {
		return finishRule(b)
	}
	level := canonicalLevel(a.level)
	if level == LevelGroups {
		return LevelGroups, intersectIDs(a.groups, b.groups)
	}
	return level, nil
}

func finishRule(r accessRule) (string, []string) {
	level := canonicalLevel(r.level)
	if level == "" {
		level = LevelPublic
	}
	if level != LevelGroups {
		return level, nil
	}
	return level, append([]string(nil), r.groups...)
}

func intersectIDs(a, b []string) []string {
	have := map[string]bool{}
	for _, id := range a {
		if id != "" {
			have[id] = true
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range b {
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
	var rule accessRule
	for _, gr := range g.st.Snapshot().AppGrants {
		if gr.Container != container || strings.TrimSpace(gr.Level) == "" {
			continue
		}
		rule = accessRule{set: true, level: gr.Level}
		if canonicalLevel(gr.Level) == LevelGroups {
			rule.groups = g.ResolveGroups(gr.GroupIDs)
		}
	}
	return rule
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
	rule := accessRule{set: true, level: canonicalLevel(access)}
	if rule.level == LevelGroups {
		rule.groups = g.ResolveGroups(groups)
	}
	return rule
}

// Effective is the rule a web route and the directory both show: the stricter
// of the Citizen ID grant and the publish toggle.
func (g *Gateway) Effective(container, host string) (string, []string) {
	level, groups := combineRules(g.grantRule(container), g.publishRule(container, host))
	if groups == nil {
		groups = []string{}
	}
	return level, groups
}

// EffectivePublish combines a publish record the caller already has with the
// stored Citizen ID grant. The publish side is always set, even when it is public.
func (g *Gateway) EffectivePublish(container, level string, groups []string) (string, []string) {
	pub := accessRule{set: true, level: canonicalLevel(level)}
	if pub.level == LevelGroups {
		pub.groups = g.ResolveGroups(groups)
	}
	out, ids := combineRules(g.grantRule(container), pub)
	if ids == nil {
		ids = []string{}
	}
	return out, ids
}
