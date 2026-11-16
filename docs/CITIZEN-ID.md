# Citizen ID

Citizen ID is FELDCORE's identity framework: users and groups, the sign-in page, and the auth gateway in front of published apps. The package is `internal/citizenid`. The built-in administrator account stays bcrypt. When an authenticator is enrolled, that sign-in asks for the code too.

There is no separate database. Accounts, groups, app rules, passkeys, and sessions live in the state file. Authenticator secrets live in the secrets box (`totp-<id>`, and `totp-p-<id>` while a new authenticator is waiting to be confirmed). The administrator's id is the literal `admin`.

## What the proxy calls

```go
func (g *Gateway) Authorize(r *http.Request, app AppAccess) (Decision, error)
func (g *Gateway) Middleware(app AppAccess) func(http.Handler) http.Handler
func (g *Gateway) ServeCheck(w http.ResponseWriter, r *http.Request) // GET /citizen/check
```

`AppAccess` is one published app:

| Field | Meaning |
| --- | --- |
| `ID` | Container name, the same id the proxy already uses |
| `Name`, `Host` | Shown in the audit log and the sign-in redirect |
| `Level` | `public`, `users`, `groups`, or `admin`. Empty means `public` |
| `GroupIDs` | Group ids when the level is `groups` |

`Decision`:

| Field | Meaning |
| --- | --- |
| `Allow` | The request may continue |
| `Reason` | `public`, `ok`, `unauthenticated`, or `forbidden` |
| `Identity` | The signed-in person, when there is one |
| `Location` | `/citizen/signin?next=...` when a browser should sign in |

`public` is allowed with no cookie. `users` is any signed-in person, including the administrator. `groups` is membership in one of the listed groups, or the administrator. An empty group list denies everyone who is not the administrator. `admin` is the administrator only. Any other level is closed, including for the administrator: `Gated` fails closed on a level it does not know. The administrator may open `public`, `users`, `groups`, and `admin`.

A gated app served without TLS is refused for a client that is not on the local network. Loopback, private, and link-local addresses may still open the sign-in page.

`GET /citizen/check` is the forward_auth target. It reads `X-Forwarded-Host` and answers:

- **200** when the cookie may open that app
- **302** to `/citizen/signin?next=<original url>` when nobody is signed in
- **403** when someone is signed in and the rule excludes them

Anonymous challenges are not written one-by-one. Failures for an unknown username are one aggregated "sign-in noise" line per address per minute, and that noise is dropped before a security event when the log is full. A denial of a known person is recorded, as is each sign-in and each failed sign-in of a known account.

## Cookies

Three cookies, on purpose. A published app must not be able to replay the sign-in cookie against the management API.

| Cookie | Where it is sent | What it unlocks |
| --- | --- | --- |
| `feldcore_citizen` | Parent domain, so every published app | App access only: forward_auth, the publish listener, and `/citizen/check`. Never `/api/v1`. |
| `feldcore_desk` | Host-only, SameSite `Strict` | The launcher and a person's own sign-in settings. Not administrator routes. |
| `feldcore_session` | Host-only, SameSite `Strict` | The management API, with a bearer token as the other way in. |

Each is HttpOnly and Path `/`. The value stored in the state file is a hash of the token, and the session is bound to the account id, so recreating a username does not revive a deleted person's session. `Secure` is set when the request is TLS or `X-Forwarded-Proto` is `https`. On that same HTTPS request, a host-only cookie is written with the `__Host-` prefix (`__Host-feldcore_citizen` only when no Domain is set, plus `__Host-feldcore_desk` and `__Host-feldcore_session`). A parent-domain cookie cannot use `__Host-`.

When the request host is the parent domain (Settings → Network) or a subdomain of it, `feldcore_citizen` uses that parent as its Domain, with no leading dot. Localhost, IP addresses, and unrelated hosts get a host-only cookie. The management UI's own hostname is not put behind forward_auth.

Lifetime follows the session timeout in Settings (1–720 hours), otherwise 7 days. Changing a password or disabling an account bumps that account's session version, which signs it out. At the 2,000-session cap the oldest extra session of the busiest account is dropped, never the one just created.

## Caddy and the tunnel

A route is gated only when its level is `users`, `groups`, or `admin`. The default is public, so an existing hostname stays open until an administrator sets a rule. An unknown level is gated and then denied. `route` keeps source order, so the sign-in page itself is not challenged:

```
route {
	reverse_proxy /citizen/* host.docker.internal:PORT
	forward_auth host.docker.internal:PORT {
		uri /citizen/check
		copy_headers Cookie
		header_up X-Forwarded-Host {http.request.host}
		header_up X-Forwarded-Uri {http.request.uri}
		header_up X-Forwarded-Proto {http.request.scheme}
	}
	reverse_proxy UPSTREAM {
		header_up -Authorization
		header_up Cookie {clean_cookie}
	}
}
```

`clean_cookie` is the browser's Cookie header with `feldcore_citizen`, `feldcore_desk`, and `feldcore_session` (and their `__Host-` names) removed. The app keeps its own cookies. When the upstream is the publish listener on port 8791, Caddy leaves the cookie in place and that listener strips it before dialing the container.

Cloudflare Tunnel ingress for every published app points at that listener (`http://127.0.0.1:8791`, or `host.docker.internal:8791` when the connector runs in Docker). It does not dial the container, so a gated app cannot be opened from the internet by skipping Citizen ID. The publish listener runs the same access check as forward_auth, including group membership.

`next` is a same-origin path, or an http(s) URL whose host is this management UI, an enabled web route, or an enabled published app. Control characters, backslashes, `//`, and a scheme are rejected after decoding. A parent-domain suffix on its own is not enough.

Saving an app's access level writes the grant and applies the proxy. If the proxy cannot reload, the save reports that failure.

## Sign-in

The browser UI and `GET`/`POST /citizen/signin` both say Citizen ID. Passwords for new accounts are argon2id. The administrator password stays bcrypt, so existing hashes keep working. Every password check runs one bcrypt comparison and one argon2id comparison, so an unknown name does not return faster than a real account.

When an authenticator is enrolled, both Citizen ID sign-in and `POST /api/v1/auth/login` ask for the TOTP code. A code is accepted once: the last used time-step is stored and a replay of it is rejected. A wrong code counts toward lockout. A missing code, once the password has been accepted, does not. Replacing or removing an authenticator or a passkey requires the password and the current code again. A signed-in session is not enough.

Passkeys use WebAuthn discoverable login and registration while signed in. Login asks the authenticator for user verification (`required`), so a passkey used on its own is not a silent presence check. They need localhost or HTTPS. An IP address is refused with a message asking for a hostname. Ceremony state is capped by dropping the soonest expiry, not by wiping the rest.

The HTML form uses a synchronizer CSRF cookie (`feldcore_csrf` on `/citizen`). JSON under `/api/v1` keeps the existing same-origin check. A pending second factor for the HTML form is a short-lived cookie bound to the client address, so the form does not echo the password.

Lockout is 8 failures in 5 minutes, counted per username and per real client address. `X-Forwarded-For` and `CF-Connecting-IP` are trusted only when the TCP peer is loopback, Caddy, or cloudflared. A successful sign-in clears the username bucket and leaves the address bucket in place. Concurrent attempts share one reservation, and argon2id hashing is capped at four at a time.

A directory user who signs in receives the desk cookie and can open the App Launcher and their own authenticator and passkeys. The citizen cookie on its own is not a management session. The administrator signs in through Citizen ID as well and receives the management session cookie, then can open Settings → Citizen ID to create and disable users, reset passwords, reset authenticators, assign groups, set each app's access level, and edit the sign-in page.

## Sign-in page

Settings → Citizen ID has the default page: title, welcome text, logo, accent colour, background, footer, and up to four links. Each published app can override those fields on its Publish form, with a live preview. A blank field uses the default. A logo or background image is a PNG, JPEG, or WebP data URL of at most 80 KB. Colours are `#rrggbb`. Links start with `https://` or `/`. `GET /api/v1/citizen/brand` returns the page for a host. `PUT /api/v1/citizen/defaults` stores the default.

## Adding another provider

```go
type IdentityProvider interface {
    Name() string
    Authenticate(username, password, code string) (Identity, error)
}
```

`SetProvider` replaces the local directory. `Authorize` does not change. Entra ID / OIDC and Cloudflare Access are not built. Passkeys belong to the local provider.
