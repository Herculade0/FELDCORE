package publish

import "net/http"

// Access levels stored on a publication. Citizen ID enforces them. This
// package only records the choice and hands it to AuthHook.
const (
	AccessPublic   = "public"
	AccessSignedIn = "signed-in"
	AccessGroups   = "groups"
	AccessAdmins   = "admins"
)

// App is one published container, as the proxy chain sees it.
// Groups is set only when Access is AccessGroups.
type App struct {
	Container string
	Subdomain string
	Hostname  string
	Port      int
	Access    string
	Groups    []string
}

// DenyError is a refusal from an AuthHook. Status 0 means 403.
// Citizen ID returns this when the stored access level is not met.
// Location, when set, is the sign-in page a browser should open.
type DenyError struct {
	Status   int
	Msg      string
	Location string
}

func (e *DenyError) Error() string {
	if e == nil || e.Msg == "" {
		return "Citizen ID refused this request."
	}
	return e.Msg
}

// AuthHook is the Citizen ID seam in the publish proxy chain.
//
// The chain is fixed: route lookup, then protection, then AuthHook.Allow,
// then the reverse proxy to the app. Citizen ID implements AuthHook and is
// installed with Service.SetAuth. A nil hook and AllowAll both allow every
// request, so access levels can be stored before that package is wired in.
// Allow receives the publication, including Access and Groups, and does not
// need to read the store itself.
type AuthHook interface {
	Allow(r *http.Request, app App) error
}

// AllowAll is the default AuthHook. It does not enforce access levels.
type AllowAll struct{}

// Allow implements AuthHook.
func (AllowAll) Allow(*http.Request, App) error { return nil }

// AccessLabel is the Citizen ID wording shown in the UI and the overview.
func AccessLabel(access string) string {
	switch access {
	case AccessPublic, AccessSignedIn, AccessGroups, AccessAdmins:
	default:
		access = AccessAdmins
	}
	return "Citizen ID: " + access
}
