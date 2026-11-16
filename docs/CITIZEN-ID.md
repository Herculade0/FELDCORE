# Citizen ID

Citizen ID is FELDCORE's identity framework: users and groups, the sign-in page, and the auth gateway in front of published apps. The package is `internal/citizenid`. The built-in administrator account and its original sign-in stay as they are.

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

`public` is allowed with no cookie. `users` is any signed-in person, including the administrator. `groups` is membership in one of the listed groups, or the administrator. An empty group list denies everyone who is not the administrator. `admin` is the administrator only. Any other level is closed. The administrator may open every level that is not public.

`GET /citizen/check` is the forward_auth target. It reads `X-Forwarded-Host` and answers:

- **200** when the cookie may open that app
- **302** to `/citizen/signin?next=<original url>` when nobody is signed in
- **403** when someone is signed in and the rule excludes them

Anonymous challenges are not written to the audit log. A denial of a known person is, as is each sign-in and each failed sign-in.

## Cookie

The session cookie is `feldcore_citizen`. It is HttpOnly, Path `/`, SameSite `Lax`, and persistent. The value stored in the state file is a hash of the token. `Secure` is set when the request is TLS or `X-Forwarded-Proto` is `https`.

When the request host is the parent domain (Settings → Network) or a subdomain of it, the cookie Domain is that parent, with no leading dot, so one sign-in covers every published app. Localhost, IP addresses, and unrelated hosts get a host-only cookie. The management UI's own hostname is not put behind forward_auth; it keeps its own login.

Lifetime follows the session timeout in Settings (1–720 hours), otherwise 7 days. Changing a password or disabling an account bumps that account's session version, which signs it out. The original administrator cookie does not consult that version.

## Caddy

A route is gated only when its level is `users`, `groups`, or `admin`. The default is public, so an existing hostname stays open until an administrator sets a rule. `route` keeps source order, so the sign-in page itself is not challenged:

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
	reverse_proxy UPSTREAM
}
```

`PORT` is the daemon's listen port. `next` is accepted only when it is a relative path, or an http(s) URL on this host, the parent domain, a subdomain of it, or a published hostname.

## Sign-in

The browser UI and `GET`/`POST /citizen/signin` both say Citizen ID. Passwords for new accounts are argon2id. The administrator password stays bcrypt, so existing hashes keep working.

When an authenticator is enrolled, Citizen ID sign-in (`POST /api/v1/citizen/login`) asks for the TOTP code. The original `POST /api/v1/auth/login` does not. A wrong code counts toward lockout. A missing code, once the password has been accepted, does not.

Passkeys use WebAuthn discoverable login and registration while signed in. They need localhost or HTTPS. An IP address is refused with a message asking for a hostname.

The HTML form uses a synchronizer CSRF cookie (`feldcore_csrf` on `/citizen`). JSON under `/api/v1` keeps the existing same-origin check. A pending second factor for the HTML form is a short-lived cookie bound to the client address, so the form does not echo the password.

Lockout is 8 failures in 5 minutes, counted per username and per TCP peer (`RemoteAddr` only, the same rule as the administrator login). Behind Caddy every browser shares the proxy's address, so one noisy client can lock the proxy's address as well as that username.

A directory user who signs in can open the App Launcher and their own authenticator and passkeys. The management API answers 403. The administrator, including a Citizen ID session for that account, can open Settings → Citizen ID to create and disable users, reset passwords, reset authenticators, assign groups, and set each app's access level.

## Adding another provider

```go
type IdentityProvider interface {
    Name() string
    Authenticate(username, password, code string) (Identity, error)
}
```

`SetProvider` replaces the local directory. `Authorize` does not change. Entra ID / OIDC and Cloudflare Access are not built. Passkeys belong to the local provider.
