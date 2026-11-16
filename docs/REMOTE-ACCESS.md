# Remote access, names, and HTTPS

FELDCORE can publish this server without opening ports on your router, and it can give each container a name on your home network. Both are off until you turn them on. A fresh install exposes nothing.

## Remote access

Open **Settings → Remote access**.

The connector is [cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/). FELDCORE prefers to run it as the container `feldcore-cloudflared`, on a Docker network named `feldcore`, so it stays under FELDCORE's control and is not mixed into the host packages. If Docker is not running, use **Install cloudflared on the host**. That downloads the official binary into FELDCORE's data directory and runs it only while remote access is on.

### Tunnel token

In the Cloudflare dashboard: **Zero Trust → Networks → Tunnels → Create**, then copy the install token.

Paste it and choose **Save and connect**. The token is encrypted with a key that lives in `/var/lib/feldcore/secrets/` (mode `0600`) and is never written to the log, the state file, or the container's command line. The running connector reads it from a mode `0600` file mounted into the container.

A tunnel token only connects this server. It cannot create DNS records. Add an API token as well if you want FELDCORE to publish hostnames for you. Otherwise add the hostnames in the Cloudflare dashboard.

### API token

Create a token at <https://dash.cloudflare.com/profile/api-tokens> with:

- Account / Cloudflare Tunnel / Edit
- Account / Account Settings / Read (so FELDCORE can see which account to use)
- Zone / DNS / Edit
- Zone / Zone / Read

Choose **API token**. FELDCORE creates a remotely managed tunnel named `feldcore` (or the name you set), stores the connector token the same way as a pasted token, starts cloudflared, and from then on writes ingress rules and DNS itself.

If the token can see more than one account, the page asks for the account ID.

### Expose a container

Nothing is published by default. On the container, open **Web address** and turn on **Expose publicly**. Set a hostname on a domain Cloudflare already hosts, such as `plex.example.com`, and the port the app's web page listens on inside the container.

FELDCORE then:

1. Attaches the container to the `feldcore` network (unless it uses the host network).
2. Writes a tunnel ingress rule pointing at `http://<container>:<port>`.
3. Creates or updates a proxied CNAME to `<tunnel-id>.cfargotunnel.com`.

Turning the switch off removes that rule and deletes the DNS record FELDCORE created. The last ingress rule is always `http_status:404`, so an unknown hostname does not fall through to another app.

These names are for web pages. A game or UDP port is not carried by the tunnel.

### The FELDCORE page itself

**Expose the FELDCORE page** is off. Turning it on publishes the admin UI at the hostname you set.

Do that only together with [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/access-controls/policies/). In the dashboard: **Zero Trust → Access → Applications**, and require an identity check before the request is allowed to reach FELDCORE's login. Without Access, anyone who can open the hostname can try passwords against this server.

`.home.arpa`, `.local`, `.lan`, and `.internal` names cannot be published. Cloudflare has no DNS zone for them.

## Friendly names and HTTPS

Open **Settings → Network → Web addresses**.

The proxy is [Caddy](https://caddyserver.com/), running as the container `feldcore-caddy`. Caddy is the right tool here for three reasons:

- It obtains and renews certificates by itself, including an internal certificate authority for names that are not on the public internet.
- The same config file switches one site to Let's Encrypt without running a second proxy.
- It stays a container. A crash in the proxy does not take `feldcored` down, and FELDCORE can replace it the same way it replaces any other container it owns.

The stock `caddy:2` image already does the internal authority, so a home network does not wait on a custom build. Public certificates use the DNS-01 challenge and the module `caddy-dns/cloudflare`, which the stock image does not include. The first time you ask for Let's Encrypt, FELDCORE builds a local image `feldcore/caddy:2` from a short Dockerfile that adds that module. The API token is passed to the container as `CF_API_TOKEN`. The Caddyfile only contains `{env.CF_API_TOKEN}`, never the token itself.

### On your home network

On a container, open **Web address**, turn on **Name on your home network**, and accept the suggested hostname. Suggestions come from the App Store template: a WebUI value of `http://[IP]:[PORT:2456]/` becomes `valheim.home.arpa` on port 2456 when the home domain is `home.arpa`.

**HTTPS with the local certificate** is the default. Browsers will warn until they trust FELDCORE's root. Download it from the web addresses card after Caddy has started (it is written under the proxy data folder as `pki/authorities/local/root.crt`) and install it:

- Windows: double-click, Local machine, Trusted Root Certification Authorities.
- Mac: System keychain, then Always Trust.
- iPhone: install the profile, then enable it under Settings → General → About → Certificate Trust Settings.
- Android: Settings → Security → Install a certificate → CA certificate.
- Linux: copy to `/usr/local/share/ca-certificates/feldcore.crt` and run `update-ca-certificates`.

**Plain HTTP** skips certificates. Use it only on a network you trust.

### On a public domain, without the tunnel

Choose **HTTPS with Let's Encrypt** and a hostname on a zone Cloudflare hosts, such as `app.example.com`. Caddy solves the DNS-01 challenge with the same API token as Remote access. The server does not need port 80 open to the internet for the certificate. It does still need port 443 reachable by whoever should open the name, unless you are using the tunnel instead. The tunnel terminates TLS at Cloudflare, so a public tunnel hostname does not need this certificate.

The FELDCORE UI can be served the same way. Turn on **Serve this page on a hostname**.

### Ports 80 and 443

Caddy publishes ports 80 and 443 on the host. Before it starts, FELDCORE checks two things:

- other containers that publish those ports
- other programs listening on them (`/proc/net/tcp`)

`feldcore-caddy` itself is not treated as a conflict. If something else holds a port, the page names it and does not start the proxy. Stop that container or change the host port it publishes, then save again. A typical message is: port 80 is already used by the container "nginx"; Caddy needs ports 80 and 443.

## What is stored

| Item | Where | Mode |
| --- | --- | --- |
| Encrypted API token and tunnel token | `/var/lib/feldcore/secrets/` | `0600` |
| Encryption key | `/var/lib/feldcore/secrets/master.key` | `0600` |
| Runtime tunnel token file | `/var/lib/feldcore/run/tunnel.token` | `0600` |
| Caddyfile and certificates | `/var/lib/feldcore/caddy/` | directory `0700` |
| Hostnames and routes | `state.json` | no secrets |

Turning remote access off stops the connector and deletes the runtime token file. It does not delete the tunnel in Cloudflare, so turning it back on uses the same tunnel.
