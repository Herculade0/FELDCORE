package citizenid

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func httpSet(w http.ResponseWriter, r *http.Request, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: requestSecure(r),
	})
}

func clearCookie(w http.ResponseWriter, name string, r *http.Request) {
	path := "/"
	if name == pendingCookie || name == csrfCookie {
		path = "/citizen"
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: path, MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: requestSecure(r),
	})
}

func (g *Gateway) issueCSRF(w http.ResponseWriter, r *http.Request) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	httpSet(w, r, csrfCookie, hashKey(token), "/citizen", 3600)
	return token, nil
}

func (g *Gateway) checkCSRF(r *http.Request, token string) bool {
	c, err := r.Cookie(csrfCookie)
	if err != nil || token == "" || c.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(hashKey(token))) == 1
}

func originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := r.Host
	if fh := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); fh != "" {
		host = fh
	}
	return strings.EqualFold(u.Host, host)
}

type signInView struct {
	CSRF     string
	Error    string
	Next     string
	Username string
	TOTP     bool
}

// ServeSignIn is the Citizen ID page. A proxy sends browsers here when an
// app requires a sign-in. The form carries a CSRF token.
func (g *Gateway) ServeSignIn(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	next := g.safeNext(r, r.FormValue("next"))
	if r.Method == http.MethodGet {
		if q := r.URL.Query().Get("next"); q != "" {
			next = g.safeNext(r, q)
		}
	}
	view := signInView{Next: next}
	if r.Method == http.MethodPost {
		if !originOK(r) || !g.checkCSRF(r, r.FormValue("csrf")) {
			http.Error(w, "This form has expired. Reload the page and try again.", http.StatusForbidden)
			return
		}
		if r.FormValue("step") == "totp" {
			id, err := g.FinishSecondFactor(w, r, r.FormValue("code"))
			if err == nil {
				http.Redirect(w, r, next, http.StatusSeeOther)
				return
			}
			view.TOTP = true
			view.Username = id.Username
			if view.Username == "" {
				if c, cerr := r.Cookie(pendingCookie); cerr == nil {
					g.mu.Lock()
					p := g.pending[hashKey(c.Value)]
					g.mu.Unlock()
					view.Username = p.Username
				}
			}
			view.Error = signInMessage(err)
		} else {
			username := strings.TrimSpace(r.FormValue("username"))
			_, err := g.Login(w, r, username, r.FormValue("password"), r.FormValue("code"))
			if err == nil {
				http.Redirect(w, r, next, http.StatusSeeOther)
				return
			}
			if errors.Is(err, ErrSecondFactor) {
				if perr := g.issuePending(w, r, username); perr != nil {
					view.Error = "Something went wrong. Try again."
				} else {
					view.TOTP = true
					view.Username = username
				}
			} else {
				view.Username = username
				view.Error = signInMessage(err)
			}
		}
	}
	token, err := g.issueCSRF(w, r)
	if err != nil {
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	view.CSRF = token
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	status := http.StatusOK
	if view.Error != "" && !view.TOTP {
		status = http.StatusUnauthorized
	}
	if errors.Is(errFromMessage(view.Error), ErrRateLimited) {
		status = http.StatusTooManyRequests
	}
	w.WriteHeader(status)
	_ = signInTmpl.Execute(w, view)
}

func signInMessage(err error) string {
	var input *InputError
	switch {
	case errors.As(err, &input):
		return input.Msg
	case errors.Is(err, ErrInvalidCredentials):
		return "Incorrect username or password."
	case errors.Is(err, ErrInvalidCode), errors.Is(err, ErrSecondFactor):
		return "That verification code is not valid."
	case errors.Is(err, ErrRateLimited):
		return "Too many failed attempts. Wait a few minutes and try again."
	case errors.Is(err, ErrDisabled):
		return "This account is disabled."
	default:
		return "Something went wrong. Try again."
	}
}

func errFromMessage(msg string) error {
	if msg == "Too many failed attempts. Wait a few minutes and try again." {
		return ErrRateLimited
	}
	return nil
}

var signInTmpl = template.Must(template.New("signin").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Citizen ID · FELDCORE</title>
<style>
  :root { color-scheme: light; --bg:#f4f7fd; --ink:#0a1633; --ink2:#43506b; --ink3:#8592ad; --line:rgba(11,40,110,.12); --accent:#0b5cff; --accent2:#38bdf8; --bad:#f04438; }
  * { box-sizing: border-box; }
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center; padding:32px 16px;
    font-family: Manrope, ui-rounded, ui-sans-serif, system-ui, sans-serif; color:var(--ink); background:
    radial-gradient(58rem 38rem at 8% -12%, rgba(11,92,255,.18), transparent 62%),
    radial-gradient(52rem 40rem at 104% 4%, rgba(56,189,248,.16), transparent 60%), var(--bg); }
  form { width:100%; max-width:26rem; background:rgba(255,255,255,.82); border:1px solid var(--line); border-radius:2rem; padding:1.75rem;
    box-shadow:0 12px 32px -16px rgba(16,24,40,.25); }
  .brand { display:flex; align-items:center; gap:.75rem; margin:0 0 1.25rem; }
  h1 { font-size:1.35rem; margin:0; letter-spacing:-.02em; }
  .eyebrow { margin:.35rem 0 0; font-size:.68rem; font-weight:700; letter-spacing:.18em; text-transform:uppercase; color:var(--ink3); }
  p.lead { margin:.7rem 0 0; color:var(--ink2); font-size:.92rem; line-height:1.45; }
  label { display:block; margin:1rem 0 .35rem; font-size:.75rem; font-weight:700; color:var(--ink2); }
  input { width:100%; border:1px solid var(--line); background:rgba(255,255,255,.7); border-radius:.85rem; padding:.7rem .9rem; font:inherit; color:var(--ink); }
  button { width:100%; margin-top:1.1rem; border:0; border-radius:.85rem; padding:.75rem 1rem; font:inherit; font-weight:800; color:white; cursor:pointer;
    background:linear-gradient(135deg, var(--accent), var(--accent2)); }
  button.secondary { background:transparent; color:var(--ink); border:1px solid var(--line); margin-top:.6rem; }
  .err { margin-top:1rem; border-radius:.85rem; padding:.7rem .85rem; background:rgba(240,68,56,.1); color:var(--bad); font-size:.85rem; font-weight:650; }
  .mark { width:42px; height:42px; }
</style>
</head>
<body>
<form method="post" action="/citizen/signin" data-next="{{.Next}}">
  <div class="brand">
    <svg class="mark" viewBox="0 0 32 32" aria-hidden="true"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0%" stop-color="#0b5cff"/><stop offset="100%" stop-color="#38bdf8"/></linearGradient></defs><polygon points="16,1.5 28.6,8.75 28.6,23.25 16,30.5 3.4,23.25 3.4,8.75" fill="url(#g)"/><polygon points="16,10.5 20.76,13.25 20.76,18.75 16,21.5 11.24,18.75 11.24,13.25" fill="#fff"/></svg>
    <div>
      <h1>Citizen ID</h1>
      <p class="eyebrow">FELDCORE</p>
    </div>
  </div>
  {{if .TOTP}}
  <p class="lead">Enter the 6-digit code from your authenticator for {{.Username}}.</p>
  <input type="hidden" name="step" value="totp">
  <label for="code">Authenticator code</label>
  <input id="code" name="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9 ]{6,8}" required autofocus>
  {{else}}
  <p class="lead">One sign-in for this server and the apps published on it.</p>
  <label for="username">Username</label>
  <input id="username" name="username" autocomplete="username" autocapitalize="none" spellcheck="false" required autofocus value="{{.Username}}">
  <label for="password">Password</label>
  <input id="password" name="password" type="password" autocomplete="current-password" required>
  {{end}}
  <input type="hidden" name="csrf" value="{{.CSRF}}">
  <input type="hidden" name="next" value="{{.Next}}">
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <button type="submit">{{if .TOTP}}Verify{{else}}Sign in{{end}}</button>
  {{if not .TOTP}}
  <button class="secondary" type="button" id="passkey">Sign in with a passkey</button>
  {{end}}
</form>
<script>
(function () {
  var btn = document.getElementById("passkey");
  if (!btn || !window.PublicKeyCredential) { if (btn) btn.hidden = true; return; }
  function b64urlToBuf(s) {
    s = s.replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) s += "=";
    var bin = atob(s), out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out.buffer;
  }
  function bufToB64url(buf) {
    var bytes = new Uint8Array(buf), s = "";
    for (var i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
    return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
  }
  btn.addEventListener("click", function () {
    btn.disabled = true;
    fetch("/citizen/webauthn/login/begin", { method: "POST", credentials: "same-origin" })
      .then(function (res) { if (!res.ok) throw new Error("Passkeys are not available on this address."); return res.json(); })
      .then(function (opts) {
        var pk = opts.publicKey;
        pk.challenge = b64urlToBuf(pk.challenge);
        (pk.allowCredentials || []).forEach(function (c) { c.id = b64urlToBuf(c.id); });
        return navigator.credentials.get({ publicKey: pk });
      })
      .then(function (cred) {
        var body = {
          id: cred.id, rawId: bufToB64url(cred.rawId), type: cred.type,
          response: {
            clientDataJSON: bufToB64url(cred.response.clientDataJSON),
            authenticatorData: bufToB64url(cred.response.authenticatorData),
            signature: bufToB64url(cred.response.signature),
            userHandle: cred.response.userHandle ? bufToB64url(cred.response.userHandle) : null
          },
          clientExtensionResults: cred.getClientExtensionResults()
        };
        return fetch("/citizen/webauthn/login/finish", {
          method: "POST", credentials: "same-origin",
          headers: { "Content-Type": "application/json" }, body: JSON.stringify(body)
        });
      })
      .then(function (res) { if (!res.ok) throw new Error("That passkey could not be verified."); location.assign(document.querySelector("form").dataset.next || "/"); })
      .catch(function (err) {
        btn.disabled = false;
        var box = document.querySelector(".err");
        if (!box) { box = document.createElement("div"); box.className = "err"; btn.parentNode.insertBefore(box, btn); }
        box.textContent = err.message || "That passkey could not be verified.";
      });
  });
})();
</script>
</body>
</html>
`))
