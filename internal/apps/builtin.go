package apps

import (
	_ "embed"
	"strings"

	"github.com/Herculade0/feldcore/internal/bridge"
	"github.com/Herculade0/feldcore/internal/docker"
)

//go:embed templates/proton-bridge.xml
var protonBridgeXML []byte

const (
	builtinCategoryID    = "AI"
	builtinCategoryLabel = "AI integration"
)

// protonTemplate is the embedded Unraid container template. A parse failure
// is kept so the catalogue still loads and the spec call can say why.
var (
	protonTemplate docker.Template
	protonParseErr error
)

func init() {
	protonTemplate, protonParseErr = docker.ParseTemplateXML(protonBridgeXML)
}

func protonRecord() (appRecord, []string, bool) {
	if protonParseErr != nil || protonTemplate.Repository == "" {
		return appRecord{}, nil, false
	}
	rec := appRecord{
		tmpl: protonTemplate,
		app: App{
			ID:         bridge.AppID,
			Name:       "Proton Bridge",
			Author:     "FELDCORE",
			Image:      protonTemplate.Repository,
			Overview:   "Headless Proton Mail Bridge for AI integration. Keeps an encrypted Proton mailbox on this server and publishes IMAP and SMTP, with a Bridge username and app password, for other FELDCORE apps and bots.",
			Categories: []string{builtinCategoryID},
			Project:    "https://github.com/shenxn/protonmail-bridge-docker",
			Support:    "https://github.com/shenxn/protonmail-bridge-docker",
			Requires:   "A paid Proton plan. Bridge is not included with Proton Free. The first sign-in is a password and a 2FA code in the container console.",
			Purpose:    bridge.Purpose,
			Origin:     "feldcore",
		},
	}
	return rec, protonNotes(), true
}

func protonNotes() []string {
	return []string{
		"Purpose: AI integration. After you apply, open Docker and choose Mailbox access on this container. That panel is where you sign in and where other apps read the IMAP and SMTP addresses.",
		"A paid Proton plan is required. Mail Plus, Unlimited, Family, Duo, and Visionary include Bridge. Proton Free does not.",
		"Sign-in is interactive. Proton asks for the account password and a 2FA code in the container console. FELDCORE cannot do that step for you.",
		"The Bridge data folder holds the keychain. It is what keeps the login across restarts. If Proton forces a new login, sign in again from Mailbox access and leave the folder in place.",
		"IMAP and SMTP are published on the LAN when the bind address is blank. Set 127.0.0.1 on each port to keep them on this server. Use a firewall if this machine has a public address.",
	}
}

func builtinRecords() []appRecord {
	rec, _, ok := protonRecord()
	if !ok {
		return nil
	}
	return []appRecord{rec}
}

func mergeBuiltins(apps []App, categories []Category) ([]App, []Category) {
	recs := builtinRecords()
	if len(recs) == 0 {
		if apps == nil {
			return []App{}, categories
		}
		return apps, categories
	}
	skip := map[string]bool{}
	out := make([]App, 0, len(apps)+len(recs))
	for _, rec := range recs {
		skip[rec.app.ID] = true
		out = append(out, rec.app)
	}
	for _, a := range apps {
		if skip[a.ID] {
			continue
		}
		out = append(out, a)
	}
	if categories == nil {
		categories = []Category{}
	}
	for _, rec := range recs {
		for _, id := range rec.app.Categories {
			categories = addCategory(categories, id, builtinCategoryLabel, 1)
		}
	}
	return out, categories
}

func addCategory(cats []Category, id, label string, n int) []Category {
	for i := range cats {
		if cats[i].ID == id {
			cats[i].Count += n
			if cats[i].Label == "" {
				cats[i].Label = label
			}
			return cats
		}
	}
	return append(cats, Category{ID: id, Label: label, Count: n})
}

func builtinNote(msg string, missing, offline bool, haveBuiltin bool) string {
	if !haveBuiltin || (!missing && !offline) {
		return msg
	}
	if strings.Contains(msg, "FELDCORE apps") {
		return msg
	}
	const note = "FELDCORE apps are listed below."
	if msg == "" && missing && !offline {
		return "The Community Applications catalogue hasn't been downloaded yet. " + note
	}
	if msg == "" {
		return note
	}
	return strings.TrimRight(strings.TrimSpace(msg), ".") + ". " + note
}
