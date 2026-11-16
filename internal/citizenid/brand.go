package citizenid

import (
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Herculade0/feldcore/internal/store"
)

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

const (
	defaultTitle   = "Citizen ID"
	defaultWelcome = "One sign-in for this server and the apps published on it."
	defaultAccent  = "#0b5cff"
	defaultBg      = "#f4f7fd"
)

// CleanBrand checks a sign-in page. Empty fields are kept empty so they
// fall back to the Citizen ID default. A logo or background image is a
// small PNG, JPEG or WebP data URL.
func CleanBrand(in store.SignInBrand) (store.SignInBrand, error) {
	out := store.SignInBrand{
		Title:   clip(strings.TrimSpace(in.Title), 60),
		Welcome: clip(strings.TrimSpace(in.Welcome), 240),
		Footer:  clip(strings.TrimSpace(in.Footer), 200),
	}
	if in.Accent != "" {
		if !colorRe.MatchString(strings.TrimSpace(in.Accent)) {
			return store.SignInBrand{}, &InputError{"Accent colour must be a hex colour such as #0b5cff."}
		}
		out.Accent = strings.ToLower(strings.TrimSpace(in.Accent))
	}
	if in.Background != "" {
		bg := strings.TrimSpace(in.Background)
		if colorRe.MatchString(bg) {
			out.Background = strings.ToLower(bg)
		} else {
			img, err := cleanImage(bg)
			if err != nil {
				return store.SignInBrand{}, &InputError{"Background must be a hex colour or a PNG, JPEG or WebP image."}
			}
			out.Background = img
		}
	}
	if in.Logo != "" {
		img, err := cleanImage(strings.TrimSpace(in.Logo))
		if err != nil {
			return store.SignInBrand{}, &InputError{"Logo must be a PNG, JPEG or WebP image."}
		}
		out.Logo = img
	}
	for _, link := range in.Links {
		label := clip(strings.TrimSpace(link.Label), 40)
		raw := strings.TrimSpace(link.URL)
		if label == "" && raw == "" {
			continue
		}
		if label == "" || raw == "" {
			return store.SignInBrand{}, &InputError{"Each footer link needs a label and an address."}
		}
		if strings.ContainsAny(raw, " \t\r\n\\") || strings.HasPrefix(raw, "//") {
			return store.SignInBrand{}, &InputError{"That link is not allowed."}
		}
		if !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "/") {
			return store.SignInBrand{}, &InputError{"Links must start with https:// or /."}
		}
		if strings.HasPrefix(raw, "/") && (strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\")) {
			return store.SignInBrand{}, &InputError{"That link is not allowed."}
		}
		out.Links = append(out.Links, store.BrandLink{Label: label, URL: raw})
		if len(out.Links) == 4 {
			break
		}
	}
	return out, nil
}

func cleanImage(s string) (string, error) {
	if len(s) > 80*1024 {
		return "", &InputError{"That image is too large."}
	}
	ok := strings.HasPrefix(s, "data:image/png;base64,") ||
		strings.HasPrefix(s, "data:image/jpeg;base64,") ||
		strings.HasPrefix(s, "data:image/webp;base64,")
	if !ok {
		return "", &InputError{"Use a PNG, JPEG or WebP image."}
	}
	i := strings.IndexByte(s, ',')
	if i < 0 {
		return "", &InputError{"That image could not be read."}
	}
	if _, err := base64.StdEncoding.DecodeString(s[i+1:]); err != nil {
		return "", &InputError{"That image could not be read."}
	}
	return s, nil
}

// SetBrand stores the default Citizen ID sign-in page.
func (g *Gateway) SetBrand(in store.SignInBrand) (store.SignInBrand, error) {
	clean, err := CleanBrand(in)
	if err != nil {
		return store.SignInBrand{}, err
	}
	err = g.st.Update(func(s *store.State) error {
		s.CitizenBrand = clean
		return nil
	})
	return clean, err
}

// BrandForHost is the sign-in page for host: the app's own brand where it
// set one, otherwise the Citizen ID default.
func (g *Gateway) BrandForHost(host string) store.SignInBrand {
	snap := g.st.Snapshot()
	host = strings.ToLower(stripPort(host))
	merged := mergeBrand(snap.CitizenBrand, store.SignInBrand{})
	for _, app := range snap.Publish.Apps {
		if app.Enabled && strings.EqualFold(app.Hostname, host) {
			merged = mergeBrand(snap.CitizenBrand, app.Brand)
			break
		}
	}
	return fillBrand(merged)
}

func (g *Gateway) brandForRequest(r *http.Request, next string) store.SignInBrand {
	host := forwardedHost(r)
	if u, err := url.Parse(next); err == nil && u.Host != "" {
		host = u.Host
	}
	return g.BrandForHost(host)
}

func mergeBrand(base, over store.SignInBrand) store.SignInBrand {
	out := base
	if over.Title != "" {
		out.Title = over.Title
	}
	if over.Welcome != "" {
		out.Welcome = over.Welcome
	}
	if over.Logo != "" {
		out.Logo = over.Logo
	}
	if over.Accent != "" {
		out.Accent = over.Accent
	}
	if over.Background != "" {
		out.Background = over.Background
	}
	if over.Footer != "" {
		out.Footer = over.Footer
	}
	if len(over.Links) > 0 {
		out.Links = append([]store.BrandLink(nil), over.Links...)
	} else {
		out.Links = append([]store.BrandLink(nil), base.Links...)
	}
	return out
}

func fillBrand(in store.SignInBrand) store.SignInBrand {
	if in.Title == "" {
		in.Title = defaultTitle
	}
	if in.Welcome == "" {
		in.Welcome = defaultWelcome
	}
	if in.Accent == "" {
		in.Accent = defaultAccent
	}
	if in.Background == "" {
		in.Background = defaultBg
	}
	return in
}

func brandCSS(b store.SignInBrand) template.CSS {
	accent := defaultAccent
	bg := defaultBg
	if colorRe.MatchString(b.Accent) {
		accent = b.Accent
	}
	extra := ""
	if colorRe.MatchString(b.Background) {
		bg = b.Background
	} else if strings.HasPrefix(b.Background, "data:image/") {
		extra = `body{background-image:url("` + b.Background + `");background-size:cover;background-position:center;}`
	}
	return template.CSS(":root{--accent:" + accent + ";--bg:" + bg + ";--accent2:" + accent + "}" + extra)
}
