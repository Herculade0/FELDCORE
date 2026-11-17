package publish

import (
	"net/http"
	"strings"
)

// stripUpstream removes Citizen ID and management credentials before a
// published app sees the request. The app's own cookies stay.
func stripUpstream(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("Cookie", stripCookies(req.Header.Get("Cookie"),
		"feldcore_citizen", "__Host-feldcore_citizen",
		"feldcore_desk", "__Host-feldcore_desk",
		"feldcore_session", "__Host-feldcore_session",
		"feldcore_passkey", "__Host-feldcore_passkey",
	))
	req.Header.Del("Authorization")
}

func stripCookies(header string, names ...string) string {
	if strings.TrimSpace(header) == "" {
		return ""
	}
	drop := map[string]bool{}
	for _, n := range names {
		drop[strings.ToLower(n)] = true
	}
	var kept []string
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, _, _ := strings.Cut(part, "=")
		if drop[strings.ToLower(strings.TrimSpace(key))] {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "; ")
}

// scrubSetCookies drops upstream Set-Cookie headers that would plant a
// FELDCORE session. An app keeps every other cookie it sets.
func scrubSetCookies(h http.Header) {
	if h == nil {
		return
	}
	vals := h.Values("Set-Cookie")
	if len(vals) == 0 {
		return
	}
	h.Del("Set-Cookie")
	for _, v := range vals {
		if reservedSetCookie(v) {
			continue
		}
		h.Add("Set-Cookie", v)
	}
}

func reservedSetCookie(v string) bool {
	name, _, _ := strings.Cut(v, "=")
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "__host-")
	switch name {
	case "feldcore_citizen", "feldcore_desk", "feldcore_session", "feldcore_passkey":
		return true
	default:
		return false
	}
}
