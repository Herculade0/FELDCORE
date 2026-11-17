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
