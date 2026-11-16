# FELDCORE-STORE

Design and scope for a FELDCORE catalogue layered on top of the Unraid Community Applications feed. This document is the specification. It does not change the daemon.

It was written against the App Store on `cursor/app-store-6a6a`. That branch caches the public catalogue, maps a template onto the container setup dialog, parses `Requires`, and stores a catalogue URL in Settings. On `main`, that work is still the open M2b item. FELDCORE-STORE starts after that App Store is in place. Until then the store has nothing to layer onto.

The private repository that holds the templates is separate from this one. Its layout, schema, and GitHub Action are specified here so that repo can be created without another design pass.

## Decisions

- FELDCORE-STORE is a second catalogue. The App Store has two toggle buttons, Community Apps and FELDCORE Store, and Settings can turn Community Apps off so only the store remains.
- The store repository is `Herculade0/feldcore-store` on the owner's own GitHub account, and it stays private.
- Authors write YAML in that repository. The daemon downloads one signed JSON feed from a GitHub Release. It does not clone the repository or parse YAML.
- There is one login for the whole feed, entered once in Settings. After that, every template installs with no further GitHub prompt. The login is a GitHub App installed only on that repository, using device flow. An OAuth app with the `repo` scope is rejected: that scope can read every private repository the account can read. Pasting a fine-grained token is the fallback, not the recommendation.
- The saved credential is encrypted, write-only, and never returned by the API.
- An override replaces a Community Apps template as a whole. The Community Apps toggle shows the fixed template in that app's slot, with a FELDCORE badge. The same template is listed in the FELDCORE Store. The broken template is not offered.
- Containers still open the existing setup dialog. Nothing is created until the user applies. Bundles and VMs get a short confirm page that lists what will be created.
- Game servers are one Games tab driven by fields in the YAML. Player count comes from a Steam A2S query or a log regex. There is no per-game code.
- SDR apps are containers with a USB device claim, matched by serial. They are not a separate kind.
- VM templates are data from the first feed. Installing one waits on M4 (libvirt). Windows and macOS templates never carry a download URL.
- Sizes in the roadmap say how much of the system moves. They are not calendar estimates.

## 1. Goals, non-goals, and Community Apps

### What the App Store does today

The behaviour the store builds on:

- The default catalogue is `https://raw.githubusercontent.com/Squidly271/AppFeed/master/applicationFeed.json`, the same file Unraid's CA plugin downloads. It already contains each template, so FELDCORE does not clone template repositories.
- `GET /api/v1/apps` reads the cache and does not touch the network. `POST /api/v1/apps/refresh` downloads. A failed download keeps the previous cache, sets `offline`, and says so. A copy older than 24 hours is marked stale. Nothing refreshes by itself.
- The first visit with an empty cache refreshes once from the page.
- The cache is `ca-feed.json` plus `ca-feed.time` in the data directory, mode `0600`, written atomically. A feed over 80MB is rejected.
- Plugins, blacklisted apps, and anything that is not a Docker image reference are dropped. About 4,400 apps remain.
- An app id is the first six bytes of SHA-256 over the template URL, hex-encoded. `Requires` is reduced to plain text (HTML, markdown links, and phpBB tags removed) and capped at 500 characters. The card and the setup dialog show that text.
- Choosing an app calls `TemplateToSpec`. Paths under `/mnt/user`, `/mnt/user0`, `/mnt/cache`, and `/mnt/diskN` are rewritten onto this server's shares, with `/mnt/user/appdata` landing on the `appdata` share. The user reviews the form and applies. The App Store itself creates nothing.
- Settings stores one catalogue URL (`prefs.feedURL`). Blank restores the public feed. The URL must be `http` or `https` and must not carry a username or password. That setting **replaces** the Community Apps feed. It is the wrong place to attach a private store.

VMs are a nav item and are not built (M4). Image pull sends no registry credentials. There is one administrator account. `state.json` is root-only, mode `0600`, and sessions are stored as hashes.

### Goals

- Add FELDCORE entries without forking, vendoring, or hiding the Community Apps catalogue.
- Give the owner a private place for templates Community Apps does a poor job of: pinned versions of broken templates, game-server profiles, app bundles, SDR apps with a chosen dongle, and VM profiles.
- Keep authoring reviewable (one YAML file per template, a pull request, a schema check) and keep the daemon's download as one authenticated request, the same shape as today's single feed.
- Reuse the container setup dialog, path rewriter, `Requires` scrubber, and offline cache. A store container is a Community Apps template that came from a different file.
- Let a later public feed be a source the user adds, without making this repository public and without mixing other people's private templates into it.

### Non-goals

- Replacing Community Apps, rehosting its 4,400 templates, or becoming the editor of that feed.
- A field-by-field patch language against CA XML. Overrides are wholesale. A patch format that tracks upstream XML is a later idea only if wholesale copies become painful to maintain.
- Installing on card click, telemetry, or a second package manager. Auto-refresh, when the owner leaves it on, only re-downloads the catalogues already configured. Docker and, later, libvirt remain the runtimes.
- Per-game Go code, a general plugin API, or docker-compose import. Compose import is already a separate M2 follow-up. A bundle is an ordered list of FELDCORE templates, not a Compose file.
- Shipping operating-system install media, product keys, or a macOS firmware recipe.
- Multi-user token ownership. There is one administrator. Tokens belong to the host.
- Editing the upstream CA blacklist. Hiding an app is a local feed entry.

### Merge order

Sources are an ordered list. The built-in source `community-apps` is always present, enabled by default, and uses the catalogue URL already stored in Settings.

1. Load Community Apps from its cache, unless that source is disabled.
2. Load each enabled FELDCORE source in the order shown in Settings.
3. Apply that source's entries in feed order: hides, then overrides, then new entries.
4. A later source wins when two FELDCORE sources name the same template id or override the same Community Apps app.

Settings has a switch, Community Apps, on by default. Turning it off stops that catalogue's download and leaves only the FELDCORE Store. The App Store then hides the Community Apps toggle. The catalogue URL is not how that is done. The URL remains the address of the Community Apps source alone, so a mirror of `applicationFeed.json` still works and a private store URL cannot wipe the 4,400 apps.

`GET /api/v1/apps` returns both catalogues in one payload, plus a `sources` array (id, name, status, updated time, stale, offline, message). Each entry has `origin`. The page switches on that field. Store entries are few next to the CA catalogue.

### Overrides

An override is a `container` or `game` entry with `replaces.communityApps`. The match is the image name without its tag. `name` is optional and, when set, is compared case-insensitively to the CA name. `feedbuild` fails the feed when the image alone hits more than one CA app and `name` is empty, so a shared base image cannot replace every template that uses it.

The replacement is the whole template. It keeps the Community Apps id, download count, and star count, and uses the FELDCORE name, overview, icon, `Requires`, and config. Support and project links in the YAML replace the CA links when they are set; when they are empty, the CA links stay. On the Community Apps toggle the card sits in the original slot, so a search for that app finds the fixed template. On the FELDCORE Store toggle the same card is listed with the other store entries. Both open the FELDCORE template. The setup dialog receives it after the same path rewrite as any other template.

Pinned versions are how a broken CA template stays fixed. The YAML sets `image` to the tag that works and may set `digest`. A later CA publish does not move the card, because the FELDCORE source is applied after CA on every refresh. The owner bumps the YAML when they want the new upstream.

`hide: true` removes the matched CA card and adds no card of its own. `hideReason` is required. It is recorded for the owner and listed under Settings ("Hidden by your store"), not shown as a store card.

### FELDCORE badge

Every card whose winning template came from a FELDCORE source gets `origin: feldcore` and a FELDCORE pill on the card. An override also gets `overrides: true` and the line "Replaces the Community Apps template". Community Apps cards have `origin: community-apps` and no pill.

The lock badge is a property of the store login, not of each app. It is drawn once in Settings and on the FELDCORE Store toggle.

### Dedupe

| Situation | Result |
| --- | --- |
| Explicit `replaces`, one CA match | One template. CA id. FELDCORE body. Badge. Shown in the Community Apps slot and in the FELDCORE Store. |
| Explicit `replaces`, no CA match | A normal FELDCORE card. The feed is still valid. The UI does not mention an override. |
| Same image-without-tag and same name as a CA app, no `replaces` | `feedbuild` fails and tells the author to add `replaces` or rename. The daemon, if handed an older feed that skipped that check, still collapses to one override rather than two cards. |
| Two entries in one feed with the same `id` | `feedbuild` fails. |
| Two sources override the same CA app | The later source wins. |
| `hide` and an override of the same CA app in one feed | `feedbuild` fails. The author picks one. |
| Game, VM, or bundle id | Never compared with CA container ids. Ids of new entries are `fc:<source-id>:<template-id>`, which cannot collide with CA's 12-hex ids. Overrides keep the CA id so an existing link still opens the replaced template. |

CA's own duplicate handling (a suffix on a hash collision) is unchanged and runs before the merge.

## 2. Repository layout and template schema

### Layout

The private repository is the source of the feed. It is `Herculade0/feldcore-store`, on the owner's normal GitHub account, and it stays private.

```
feldcore-store/
  schema/
    feldcore-store.schema.json    # the schema in this document
  templates/
    containers/                   # kind: container, including SDR apps
    games/                        # kind: game
    vms/                          # kind: vm
    bundles/                      # kind: bundle
  assets/
    icons/                        # png, svg, webp, referenced by the YAML
  keys/
    feed.pub                      # minisign public key, committed
  README.md                       # what the repo is, and the public-key fingerprint
  .github/workflows/feed.yml
```

`feed.key` is not committed. One template is one YAML document. The directory is for humans; the feed identifies templates by `id`, not by path.

A macOS template, if the owner decides to keep one, lives in this private repo only. It is omitted from any feed that is published to a public repository. See [Licensing](#licensing-and-a-public-feed).

### Versioning

Each template has `apiVersion: feldcore.store/v1` and a `version` of `MAJOR.MINOR.PATCH`. `version` is the template's revision, not the image tag and not the guest OS version. Installing records it on the container as the label `feldcore.template.version`.

The compiled feed has `schemaVersion: 1`. The daemon accepts a feed whose `schemaVersion` it knows. A higher number is rejected, the previous cache is kept, and the source says this FELDCORE is too old for that feed. Unknown `kind` values inside a version the daemon does know are skipped one by one and counted in the source message, so a future kind does not blank the source.

Adding an optional field does not bump `schemaVersion`. The CI schema is strict (`additionalProperties: false`). The daemon ignores JSON fields it does not know. A breaking rename or a change of meaning bumps `schemaVersion`, and the Action can publish the previous version beside it until the daemon catches up. That dual-publish is not part of the MVP.

### Kinds

| Kind | Installs as | Shown on |
| --- | --- | --- |
| `container` | The existing container dialog | Apps, and the SDR category when it claims a device |
| `game` | The container dialog, plus the game fields | Games |
| `bundle` | A confirm page, then a network and containers | Bundles |
| `vm` | A confirm page, then a libvirt domain, once M4 exists | VMs |

`game` includes a `container` block. The game is a container with a profile, not a second runtime. DOSBox-X is a `game` or a `container`. A QEMU FreeDOS machine is a `vm`.

### Container fields

The `container` object maps onto the Unraid template the App Store already converts.

| YAML | Template | Setup dialog |
| --- | --- | --- |
| `image` | Repository | Image |
| `digest` | Pull ref becomes `image@digest` when set. The tag stays visible. | Image, shown with the tag |
| `network` | Network | Network |
| `privileged` | Privileged | Privileged |
| `webui` | WebUI | Web UI |
| `extraParams` | ExtraParams | Extra parameters |
| `requires` | Requires, then the existing plain-text scrubber | "This app needs" |
| `restart` | Restart policy, default `unless-stopped` | Restart policy |
| config `type: path`, `target` | Container path | Path |
| config `default` or `value` | Host path. `/mnt/user/...` is rewritten as today. | Host path |
| config `type: port`, `target` | Container port | Port |
| config `default` | Host port | Host port |
| config `type: variable`, `target` | Env name | Variable |
| config `mask: true` | Mask | Value stays hidden |
| config `display` | `always`, `advanced`, or `hidden` | Same three levels |
| `files` | A script shipped in the feed's asset archive | Copied onto the host path at install, mode `0755` by default |

Authors write host paths as `/mnt/user/<share>/...` so the rewriter keeps one code path. `requires` is plain text. The scrubber still runs, so pasted markup cannot become a link.

A variable whose name looks like a password, secret, token, or key has an empty default and `mask: true`. `feedbuild` rejects a non-empty default on those names. Secrets are typed on the server, not stored in git.

`privileged: true` is allowed and makes `feedbuild` print a warning. SDR templates use a device claim instead of privileged mode.

### Game profiles

The Games tab is generic. A profile adds:

- `query.type: a2s` — `port` is the port inside the container. While the Games page is open, the daemon sends a Steam A2S_INFO query to the host UDP port published from it and reads the player count from the reply. On a host-network container the two ports are the same. The query runs only for a container this install created from that template, and only while that container is running.
- `query.type: log` — `path` is a path inside the container. The daemon maps it through that container's volume mounts to a file on the host, reads at most 64KiB from the end, and applies `pattern`. A path that is not inside a mount reports "No answer". The daemon does not `docker exec` to read the log. The pattern is compiled with Go's `regexp`, the same RE2 engine `feedbuild` uses, so a pattern cannot be catastrophic backtracking. `onlineGroup` and `maxGroup` are capture-group indexes.
- `query.type: none` — the tab still shows the server, with no player count.
- `rcon` — protocol name, port, and the masked variable that holds the password. The MVP stores those on the container. It does not open an RCON session. A later phase adds a Source RCON console. The password is never in the YAML.
- `saves` — names and container paths, with a `/mnt/user/...` default, shown on the game and mapped as ordinary volumes.
- `updates.mode: image` — "Update" pulls the image ref currently in the feed and recreates the container with the rollback the Docker editor already uses. A digest does not move until the feed does. `manual` shows the version and leaves the pull to the user.

The label `feldcore.template` is `<source-id>/<template-id>`. That is how the Games page finds which running containers it can query, and how a later "update available" check compares `feldcore.template.version` with the feed.

### Suggested starter catalogue

The first templates do not have to be games. This is a suggestion, made of things the owner runs, and any row can be swapped before it is written. Only the modded Valheim server is specified as a real template below. The others are ordinary containers or a bundle, using the same schema, and are not written out yet.

| Suggestion | Kind | What it is |
| --- | --- | --- |
| Valheim, modded | `game` | The first template. Thunderstore profile sync, BepInEx, and RCON. Specified in the next section and written out under Examples. |
| Calibre-Web Automated | `container` | The ebook library. A normal container template: image, config folder, port. No mod sync. |
| Orthanc | `container` | The DICOM server. A normal container template. |
| OpenWebRX+ | `container` | SDR receiver. Same shape as the readsb example: a web port plus a USB device claim matched by serial. |
| A media bundle | `bundle` | A prewired stack on one Docker network. The bundle example later in this document is the shape. Which members go in the owner's stack is still open. |

### First template: modded Valheim

Valheim's dedicated server has no RCON and no mod manager. The template is a `game` on top of `ghcr.io/lloesche/valheim-server` (the same image as Docker Hub `lloesche/valheim-server`). That image is maintained, installs BepInEx when `BEPINEX=true`, keeps the world under `/config/worlds_local`, and copies plugins from `/config/bepinex/plugins` into the game tree on install and update. It does not download a Thunderstore profile. An open change for that (pull request 777 on `valheim-server-docker`) is not in the image. The sync is a script this template ships, run from the image's `PRE_SERVER_RUN_HOOK` on every server start, including a restart inside the container.

The script is [docs/examples/valheim-modded.sync.sh](examples/valheim-modded.sync.sh). In the store repo it is `scripts/valheim-modded.sync.sh`, hashed in the signed feed, and the template's `files` entry installs it at `/config/feldcore/thunderstore-sync` mode `0755`. FELDCORE writes that file when the container is created. The hook always exits 0, so a Thunderstore failure still lets the server start.

On each start the script:

1. Reads `THUNDERSTORE_PROFILE`. A blank value or a value that is not a UUID leaves the current mods in place and logs a warning.
2. Fetches `https://thunderstore.io/api/experimental/legacyprofile/get/{code}/`. That is the endpoint r2modman and Thunderstore Mod Manager resolve a share code to. The body starts with `#r2modman\n` and then a base64 ZIP. The ZIP holds `export.r2x` (the mod list) and any exported config files. A timeout, a network error, or a 404 (the code is unknown or expired) logs a warning and does not change the installed mods.
3. Parses `export.r2x`. Each mod is `Author-Name` plus `version.major`, `version.minor`, and `version.patch`, and an `enabled` flag that defaults to true. Versions are taken from that file. The script never substitutes "latest".
4. Downloads each enabled mod from `https://thunderstore.io/package/download/{namespace}/{name}/{version}/`, following redirects, into `/config/feldcore/cache/{Author-Name}-{version}.zip`. A file already in the cache is reused. The namespace is the text before the first hyphen.
5. Skips a package whose Valheim community categories include `Client-side` and do not include `Server-side`, and logs it as skipped. A package tagged both is installed. A package tagged neither is installed and logged as not marked client or server. `denikson-BepInExPack_Valheim` is always installed when the profile lists it, because the pack is the runtime and is not tagged `Server-side`. The category list is `https://thunderstore.io/c/valheim/api/v1/package/`, which was about 180MB in November 2026. The script reduces it to name-to-categories, caches that for 24 hours, and does not download it again on every boot. If the list cannot be fetched and there is no cache, client-only filtering is skipped for that start, everything enabled is installed, and the log says so.
6. If any pinned download fails, the script stops before it replaces plugins, logs a warning, and starts the server with the last good set.
7. Only after every download has succeeded, it installs into `/config/bepinex/plugins/{Author-Name}/` and, when the live tree exists, into `/opt/valheim/bepinex/BepInEx/plugins/`. A BepInEx pack ZIP (one that contains a `BepInEx/` tree, including the pinned `denikson-BepInExPack_Valheim`) is overlaid onto the BepInEx directory without deleting `BepInEx/config` or the plugins folder. Zip members that escape the destination (`..`, absolute paths) are rejected.
8. Removes a plugin directory only when this script installed it on an earlier run (recorded in `/config/feldcore/managed.txt`) and the new profile no longer lists it. Worlds, backups, and files the owner copied in by hand are left alone.
9. Copies config files from the profile ZIP into `BepInEx/config`, then writes `org.tristan.rcon.cfg` last so the profile cannot blank the RCON password.

`lloesche/valheim-server` with `BEPINEX=true` may download a newer BepInExPack than the profile pins. The hook runs before each server start and overlays the pinned pack again. A server that is already running is not rewritten until the next start.

RCON is on by default. Valheim does not speak it. The plugin is **Tristan-ValheimRcon** ([Thunderstore](https://thunderstore.io/c/valheim/p/Tristan/ValheimRcon/), [source](https://github.com/Tristan-dvr/ValheimRcon)), version **1.6.4**, which was current on 16 November 2026 and had been updated on 13 November 2026. It is tagged `Server-side`. Its packets are Source RCON (`Login = 3`, `Command = 2`, response `0`), and mcrcon connects to it, so the template's `game.rcon.type` is `source`. The config file is `BepInEx/config/org.tristan.rcon.cfg`, section `1. Rcon`, keys `Port` and `Password`. An empty password makes the plugin refuse to listen, so the password field is required and masked. The plugin's own default port is 2458, the same number as Valheim's crossplay UDP port. The template publishes RCON on **2459/tcp** and writes that port into the config, and publishes 2456–2458/udp for the game, the Steam query, and crossplay.

If the profile already enables `Tristan-ValheimRcon`, `ferrinius-RCON_Next`, or `AviiNL-rcon`, the script keeps that one and does not add a second listener. The profile's version wins. `AviiNL-rcon` last updated in March 2024; the log says it is the older plugin and still follows the profile. If the profile has none of them, the script adds `Tristan-ValheimRcon` 1.6.4.

`ferrinius-RCON_Next` 1.2.3 is also maintained and is also Source RCON, and it is also server-side. It is the fallback if ValheimRcon stops tracking game updates. It is not the default: its listener stays off until `enabled` is true and the password is not the rejected default `ChangeMe`, and its own page says Valheim console commands such as `save` need the Valheim bridge rather than the core plugin alone. ValheimRcon already includes those commands (`save`, `say`, `kick`, `ban`, and others).

The FELDCORE RCON console is still a later phase. This template wires `game.rcon` now (protocol `source`, port 2459, password variable `RCON_PASSWORD`) so that console has nothing left to guess. The password is only in the container env and in `org.tristan.rcon.cfg` on the server volume. It is not in the YAML. Source RCON is not encrypted. The port is for a trusted network.

The image's admin, ban, and permit lists are `ADMINLIST_IDS`, `BANNEDLIST_IDS`, and `PERMITTEDLIST_IDS` (SteamID64, space separated). Setting one replaces the matching `adminlist.txt`, `bannedlist.txt`, or `permittedlist.txt`. FELDCORE omits the variable when the field is blank, so an empty form does not wipe a list the owner edited in the world folder. Those files live under `/config`. Backups default on, hourly, in `/config/backups`, which is the same volume and must not be placed inside `worlds_local`. `SERVER_PASS` has to be at least 5 characters or Valheim rejects it. `SERVER_PUBLIC` and `CROSSPLAY` are the public and crossplay toggles. `VALHEIM_PLUS` stays false, because the image cannot load it together with BepInEx.

A profile code is trusted input. The mods it names run inside the server. The log records the code, the mods installed, the mods skipped as client-only, and any warning. It does not record the server password or the RCON password.

### VM profiles

A VM template is the short list of hardware and media choices M4's wizard would otherwise ask for one at a time. The confirm page shows them and can still open every field. Era-correct defaults live in the template so the user does not have to know which NIC a Windows 98 guest can boot.

| Guest | Firmware | Machine | CPU | Disk | NIC | Media |
| --- | --- | --- | --- | --- | --- | --- |
| Ubuntu, Debian | UEFI or BIOS | `q35` | `host` | virtio | virtio | Cloud image, checksum, cloud-init |
| FreeDOS | BIOS | `pc` | `qemu64` | IDE | pcnet | Downloadable ISO, checksum |
| DOSBox-X | — | — | — | a folder mount | — | A container, not a VM |
| Windows 3.1 and 9x | BIOS | `pc` | `pentium` | IDE | pcnet or rtl8139 | Bring your own ISO |
| Windows 2000 and XP | BIOS | `pc` | `qemu64` | IDE | rtl8139 or e1000 | Bring your own ISO |
| Windows 10 | UEFI | `q35` | `host` | virtio or SATA | virtio | Bring your own ISO. Optional virtio-win driver ISO |
| Windows 11 | UEFI | `q35` | `host` | virtio | virtio | Bring your own ISO, TPM, optional Secure Boot, virtio-win |

`virtio-win` is the Fedora driver ISO. It is redistributable, it is not Microsoft media, and it is a `driverDisk` with a URL and a SHA-256. The schema rejects a `download` on `os: windows` and `os: macos`.

Cloud images and FreeDOS use `download` (`url`, `sha256`, `filename`). The file is saved in the `isos` share, or in a folder the user picks when that share does not exist. A file already at that path is reused when the hash matches and is left untouched when it does not. The domain is not created until the hash matches.

`cloudInit.userData` is a cloud-config document with no passwords in it. The confirm page can ask for a username and an SSH public key (`ask`) and append those. The daemon builds a NoCloud seed ISO and attaches it as a second CD.

Windows and macOS set `iso.required: true` and have no URL. The confirm button stays disabled until the user picks an ISO that is already on disk.

A macOS entry, if it exists, is the same BYO shape: firmware, machine, CPU, disk, NIC, and a note that the disk image is one the owner already has. The schema has no fields for OpenCore configuration, board ids, or serial numbers. Those stay outside the feed. See the open questions before adding even that stub to the private repo.

### Bundles

A bundle is a named list of other template ids in the same feed, a Docker network name, link variables, and shared questions.

```yaml
members:
  - id: prowlarr
    alias: prowlarr
  - id: sonarr
    alias: sonarr
links:
  - from: prowlarr
    to: sonarr
    variable: PROWLARR_URL
    value: "http://prowlarr:9696"
```

`feedbuild` checks that every member id is a `container` or a `game` in the feed, that every `from` and `to` is an alias, and that `value` is a URL whose host is the `from` alias. Member order is create order. `shared` asks once (a media folder, for example) and applies the answer to the listed member path targets. Those paths still go through the `/mnt/user` rewriter.

The installer creates the network, then the containers, and labels each with `feldcore.bundle` and `feldcore.bundle.install` set to this attempt. A member that fails to start stops the run. Containers from this attempt can be removed from the confirm page. Host folders are not deleted. Earlier, separate installs of the same bundle are left alone because they have a different install id.

### SDR device claims

An SDR app is a `container` in a category such as `SDR`. A device config carries a `claim`:

```yaml
- type: device
  name: RTL-SDR
  target: /dev/bus/usb/000/000
  claim:
    class: usb
    vendor: "0bda"
    product: "2838"
    match: serial
```

`target` in the file is a placeholder. At setup the daemon inventories USB devices from sysfs (`idVendor`, `idProduct`, `serial`) and, when udev has created it, `/dev/serial/by-id`. One free device that matches vendor and product is preselected. Several matches become a dropdown that shows the serial. The chosen node is the device that is actually passed through. The serial is stored against the container name. A later template with `match: serial` is not offered a claimed dongle unless the user ticks "Share this device". Removing the container releases the claim. `match: first-free` takes the first unclaimed match and is the right choice only when the machine has a single dongle of that kind.

OpenWebRX+, readsb/tar1090, rtl_433, AIS-catcher, and SatDump are five container templates plus claims. They do not need new runtime code beyond the inventory and the claim record. Privileged mode is not how a dongle is assigned.

### JSON Schema

`schema/feldcore-store.schema.json` in the store repo is the document below. CI validates the compiled feed against the root. CI validates each YAML template against `#/$defs/template`. Draft 2020-12.

`feedbuild` enforces the rules the schema cannot express cleanly: unique ids, `replaces` hitting at most one CA app, member ids existing, icon files existing and within 256KiB, every `files` path existing in the asset archive, no `..` in paths, SVG without `script` or an external reference, game regexes compiling, password-like variables having an empty masked default, Windows and macOS having no download URL, and asset bytes matching the hashes in the signed feed.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://feldcore.local/schema/feldcore-store.schema.json",
  "title": "FELDCORE store feed",
  "type": "object",
  "additionalProperties": false,
  "required": ["schemaVersion", "generatedAt", "commit", "entries", "assets"],
  "properties": {
    "schemaVersion": { "const": 1 },
    "generatedAt": { "type": "string", "format": "date-time" },
    "commit": { "type": "string", "pattern": "^[0-9a-f]{40}$" },
    "entries": {
      "type": "array",
      "items": { "$ref": "#/$defs/template" }
    },
    "assets": {
      "type": "object",
      "additionalProperties": { "type": "string", "pattern": "^[a-f0-9]{64}$" }
    }
  },
  "$defs": {
    "template": {
      "type": "object",
      "additionalProperties": false,
      "required": ["apiVersion", "kind", "id", "name", "version", "summary", "icon", "categories"],
      "properties": {
        "apiVersion": { "const": "feldcore.store/v1" },
        "kind": { "enum": ["container", "game", "vm", "bundle"] },
        "id": { "type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,62}$" },
        "name": { "type": "string", "minLength": 1, "maxLength": 80 },
        "version": { "type": "string", "pattern": "^[0-9]+\\.[0-9]+\\.[0-9]+$" },
        "summary": { "type": "string", "minLength": 1, "maxLength": 400 },
        "description": { "type": "string", "maxLength": 4000 },
        "icon": { "type": "string", "pattern": "^icons/([A-Za-z0-9_-]+/)*[A-Za-z0-9_-]+\\.(png|svg|webp)$" },
        "categories": {
          "type": "array",
          "minItems": 1,
          "items": { "type": "string", "minLength": 1, "maxLength": 40 }
        },
        "author": { "type": "string", "maxLength": 80 },
        "project": { "type": "string", "format": "uri" },
        "support": { "type": "string", "format": "uri" },
        "beta": { "type": "boolean" },
        "deprecated": { "type": "boolean" },
        "hide": { "type": "boolean" },
        "hideReason": { "type": "string", "minLength": 1, "maxLength": 300 },
        "replaces": { "$ref": "#/$defs/replaces" },
        "container": { "$ref": "#/$defs/container" },
        "game": { "$ref": "#/$defs/game" },
        "vm": { "$ref": "#/$defs/vm" },
        "bundle": { "$ref": "#/$defs/bundle" }
      },
      "allOf": [
        {
          "if": {
            "properties": { "kind": { "const": "container" }, "hide": { "const": true } },
            "required": ["kind", "hide"]
          },
          "then": { "required": ["replaces", "hideReason"] }
        },
        {
          "if": {
            "required": ["kind"],
            "properties": { "kind": { "const": "container" } },
            "not": { "required": ["hide"], "properties": { "hide": { "const": true } } }
          },
          "then": {
            "required": ["container"],
            "properties": { "game": false, "vm": false, "bundle": false }
          }
        },
        {
          "if": { "properties": { "kind": { "const": "game" } }, "required": ["kind"] },
          "then": {
            "required": ["container", "game"],
            "properties": { "vm": false, "bundle": false }
          }
        },
        {
          "if": { "properties": { "kind": { "const": "vm" } }, "required": ["kind"] },
          "then": {
            "required": ["vm"],
            "properties": { "container": false, "game": false, "bundle": false, "replaces": false }
          }
        },
        {
          "if": { "properties": { "kind": { "const": "bundle" } }, "required": ["kind"] },
          "then": {
            "required": ["bundle"],
            "properties": { "container": false, "game": false, "vm": false, "replaces": false }
          }
        }
      ]
    },
    "replaces": {
      "type": "object",
      "additionalProperties": false,
      "required": ["communityApps"],
      "properties": {
        "communityApps": {
          "type": "object",
          "additionalProperties": false,
          "required": ["image"],
          "properties": {
            "image": { "type": "string", "pattern": "^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,200}$" },
            "name": { "type": "string", "minLength": 1, "maxLength": 80 }
          }
        }
      }
    },
    "container": {
      "type": "object",
      "additionalProperties": false,
      "required": ["image", "configs"],
      "properties": {
        "image": { "type": "string", "pattern": "^[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,200}$" },
        "digest": { "type": "string", "pattern": "^sha256:[a-f0-9]{64}$" },
        "network": { "enum": ["bridge", "host", "none"] },
        "privileged": { "type": "boolean" },
        "webui": { "type": "string", "maxLength": 300 },
        "requires": { "type": "string", "maxLength": 500 },
        "extraParams": { "type": "string", "maxLength": 500 },
        "restart": { "enum": ["no", "unless-stopped", "always", "on-failure"] },
        "configs": { "type": "array", "items": { "$ref": "#/$defs/config" } },
        "files": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["path", "installTo"],
            "properties": {
              "path": { "type": "string", "pattern": "^scripts/[A-Za-z0-9._/-]+\\.sh$" },
              "installTo": { "type": "string", "minLength": 1, "maxLength": 300 },
              "mode": { "type": "string", "pattern": "^0[0-7]{3}$" }
            }
          }
        }
      }
    },
    "config": {
      "type": "object",
      "additionalProperties": false,
      "required": ["type", "name", "target"],
      "properties": {
        "type": { "enum": ["path", "port", "variable", "label", "device"] },
        "name": { "type": "string", "minLength": 1, "maxLength": 80 },
        "target": { "type": "string", "minLength": 1, "maxLength": 300 },
        "default": { "type": "string", "maxLength": 500 },
        "value": { "type": "string", "maxLength": 500 },
        "mode": { "type": "string", "maxLength": 8 },
        "description": { "type": "string", "maxLength": 500 },
        "display": { "enum": ["always", "advanced", "hidden"] },
        "required": { "type": "boolean" },
        "mask": { "type": "boolean" },
        "proto": { "enum": ["tcp", "udp"] },
        "claim": { "$ref": "#/$defs/claim" }
      }
    },
    "claim": {
      "type": "object",
      "additionalProperties": false,
      "required": ["class", "vendor", "product", "match"],
      "properties": {
        "class": { "const": "usb" },
        "vendor": { "type": "string", "pattern": "^[0-9a-fA-F]{4}$" },
        "product": { "type": "string", "pattern": "^[0-9a-fA-F]{4}$" },
        "match": { "enum": ["serial", "first-free"] }
      }
    },
    "game": {
      "type": "object",
      "additionalProperties": false,
      "required": ["query", "saves", "updates"],
      "properties": {
        "query": { "$ref": "#/$defs/query" },
        "rcon": { "$ref": "#/$defs/rcon" },
        "saves": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["name", "path"],
            "properties": {
              "name": { "type": "string", "minLength": 1, "maxLength": 80 },
              "path": { "type": "string", "minLength": 1, "maxLength": 300 },
              "hostDefault": { "type": "string", "maxLength": 300 }
            }
          }
        },
        "updates": {
          "type": "object",
          "additionalProperties": false,
          "required": ["mode"],
          "properties": { "mode": { "enum": ["image", "manual"] } }
        }
      }
    },
    "query": {
      "type": "object",
      "additionalProperties": false,
      "required": ["type"],
      "properties": {
        "type": { "enum": ["a2s", "log", "none"] },
        "port": { "type": "integer", "minimum": 1, "maximum": 65535 },
        "path": { "type": "string", "maxLength": 300 },
        "pattern": { "type": "string", "minLength": 1, "maxLength": 300 },
        "onlineGroup": { "type": "integer", "minimum": 1, "maximum": 9 },
        "maxGroup": { "type": "integer", "minimum": 1, "maximum": 9 }
      },
      "allOf": [
        {
          "if": { "properties": { "type": { "const": "a2s" } }, "required": ["type"] },
          "then": { "required": ["port"] }
        },
        {
          "if": { "properties": { "type": { "const": "log" } }, "required": ["type"] },
          "then": { "required": ["path", "pattern", "onlineGroup"] }
        }
      ]
    },
    "rcon": {
      "type": "object",
      "additionalProperties": false,
      "required": ["type"],
      "properties": {
        "type": { "enum": ["none", "source", "minecraft"] },
        "port": { "type": "integer", "minimum": 1, "maximum": 65535 },
        "passwordVariable": { "type": "string", "pattern": "^[A-Z][A-Z0-9_]{0,60}$" }
      }
    },
    "vm": {
      "type": "object",
      "additionalProperties": false,
      "required": ["os", "media", "firmware", "machine", "cpu", "vcpus", "ramMiB", "diskGiB", "diskBus", "nic"],
      "properties": {
        "os": { "enum": ["linux", "windows", "dos", "macos", "other"] },
        "media": { "enum": ["cloud-image", "iso"] },
        "firmware": { "enum": ["bios", "uefi"] },
        "machine": { "enum": ["pc", "q35"] },
        "cpu": { "enum": ["host", "qemu64", "pentium"] },
        "vcpus": { "type": "integer", "minimum": 1, "maximum": 64 },
        "ramMiB": { "type": "integer", "minimum": 64, "maximum": 1048576 },
        "diskGiB": { "type": "integer", "minimum": 1, "maximum": 8192 },
        "diskBus": { "enum": ["virtio", "sata", "ide"] },
        "nic": { "enum": ["virtio", "e1000", "rtl8139", "pcnet"] },
        "tpm": { "type": "boolean" },
        "secureBoot": { "type": "boolean" },
        "iso": {
          "type": "object",
          "additionalProperties": false,
          "required": ["required"],
          "properties": {
            "required": { "type": "boolean" },
            "share": { "type": "string", "maxLength": 40 },
            "filenameHint": { "type": "string", "maxLength": 120 }
          }
        },
        "download": { "$ref": "#/$defs/download" },
        "driverDisk": { "$ref": "#/$defs/download" },
        "cloudInit": {
          "type": "object",
          "additionalProperties": false,
          "required": ["userData"],
          "properties": {
            "userData": { "type": "string", "maxLength": 32768 },
            "growPart": { "type": "boolean" },
            "ask": {
              "type": "array",
              "items": { "enum": ["username", "sshPublicKey"] }
            }
          }
        },
        "notes": {
          "type": "array",
          "items": { "type": "string", "maxLength": 300 }
        }
      },
      "allOf": [
        {
          "if": { "properties": { "os": { "enum": ["windows", "macos"] } }, "required": ["os"] },
          "then": {
            "properties": { "download": false },
            "required": ["iso"]
          }
        },
        {
          "if": { "properties": { "media": { "const": "cloud-image" } }, "required": ["media"] },
          "then": { "required": ["download", "cloudInit"] }
        }
      ]
    },
    "download": {
      "type": "object",
      "additionalProperties": false,
      "required": ["url", "sha256", "filename"],
      "properties": {
        "url": { "type": "string", "pattern": "^https://[^\\s]+$", "maxLength": 500 },
        "sha256": { "type": "string", "pattern": "^[a-f0-9]{64}$" },
        "filename": { "type": "string", "pattern": "^[A-Za-z0-9._-]{1,120}$" }
      }
    },
    "bundle": {
      "type": "object",
      "additionalProperties": false,
      "required": ["network", "members"],
      "properties": {
        "network": { "type": "string", "pattern": "^[a-z][a-z0-9-]{0,30}$" },
        "members": {
          "type": "array",
          "minItems": 2,
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["id", "alias"],
            "properties": {
              "id": { "type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,62}$" },
              "alias": { "type": "string", "pattern": "^[a-z][a-z0-9-]{0,30}$" }
            }
          }
        },
        "links": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["from", "to", "variable", "value"],
            "properties": {
              "from": { "type": "string", "pattern": "^[a-z][a-z0-9-]{0,30}$" },
              "to": { "type": "string", "pattern": "^[a-z][a-z0-9-]{0,30}$" },
              "variable": { "type": "string", "pattern": "^[A-Z][A-Z0-9_]{0,60}$" },
              "value": { "type": "string", "maxLength": 300 }
            }
          }
        },
        "shared": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["id", "name", "type", "members"],
            "properties": {
              "id": { "type": "string", "pattern": "^[a-z][a-z0-9-]{0,30}$" },
              "name": { "type": "string", "minLength": 1, "maxLength": 80 },
              "type": { "enum": ["path"] },
              "default": { "type": "string", "maxLength": 300 },
              "description": { "type": "string", "maxLength": 300 },
              "members": {
                "type": "array",
                "minItems": 1,
                "items": {
                  "type": "object",
                  "additionalProperties": false,
                  "required": ["alias", "target"],
                  "properties": {
                    "alias": { "type": "string" },
                    "target": { "type": "string" }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}
```

### Examples

These are authoring YAML. `feedbuild` copies them into `entries` and fills `assets`. Checksums below are the right shape and are not the hashes of real files.

Container, overriding a Community Apps template and pinning the image:

```yaml
apiVersion: feldcore.store/v1
kind: container
id: jellyfin-pin
name: Jellyfin
version: 1.2.0
summary: Jellyfin with the config path and image tag this server actually runs.
icon: icons/jellyfin.png
categories:
  - Media
author: feldcore
project: https://jellyfin.org
replaces:
  communityApps:
    image: lscr.io/linuxserver/jellyfin
    name: Jellyfin
container:
  image: lscr.io/linuxserver/jellyfin:10.10.3
  digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  network: bridge
  webui: http://[IP]:[PORT:8096]
  requires: A folder of media, and a few minutes for the first library scan.
  restart: unless-stopped
  configs:
    - type: path
      name: Config
      target: /config
      default: /mnt/user/appdata/jellyfin
      mode: rw
      required: true
      description: Jellyfin's own settings and database.
    - type: path
      name: Media
      target: /data
      default: /mnt/user/media
      mode: ro
      description: Films and shows. Read only.
    - type: port
      name: Web
      target: "8096"
      default: "8096"
      proto: tcp
    - type: variable
      name: Config directory
      target: PUID
      default: "1000"
```

SDR container. `target` is filled from the dongle the user picks:

```yaml
apiVersion: feldcore.store/v1
kind: container
id: readsb-tar1090
name: readsb / tar1090
version: 1.0.0
summary: ADS-B decoder and the tar1090 map, bound to one RTL-SDR by serial.
icon: icons/readsb.png
categories:
  - SDR
project: https://github.com/wiedehopf/tar1090
container:
  image: ghcr.io/sdr-enthusiasts/docker-readsb-protobuf:latest
  network: bridge
  webui: http://[IP]:[PORT:8080]
  requires: An RTL-SDR dongle on the ADS-B antenna. It will not be offered to another SDR app.
  configs:
    - type: port
      name: Map
      target: "8080"
      default: "8080"
      proto: tcp
    - type: variable
      name: Latitude
      target: READSB_LAT
      default: "0"
      required: true
      description: Receiver latitude.
    - type: variable
      name: Longitude
      target: READSB_LON
      default: "0"
      required: true
    - type: device
      name: RTL-SDR
      target: /dev/bus/usb/000/000
      claim:
        class: usb
        vendor: "0bda"
        product: "2838"
        match: serial
```

First template, modded Valheim. The sync script is [docs/examples/valheim-modded.sync.sh](examples/valheim-modded.sync.sh). Player count is Steam A2S on UDP 2457. RCON is Tristan-ValheimRcon on TCP 2459:

```yaml
apiVersion: feldcore.store/v1
kind: game
id: valheim
name: Valheim
version: 1.0.0
summary: Modded Valheim dedicated server. A Thunderstore profile code installs BepInEx and those mods on every start.
icon: icons/valheim.png
categories:
  - Games
project: https://github.com/lloesche/valheim-server-docker
container:
  image: ghcr.io/lloesche/valheim-server:latest
  network: bridge
  requires: A Thunderstore or r2modman profile code for Valheim. The server password needs at least 5 characters. The RCON password is required, because ValheimRcon stays off when it is empty.
  restart: unless-stopped
  files:
    - path: scripts/valheim-modded.sync.sh
      installTo: /config/feldcore/thunderstore-sync
      mode: "0755"
  configs:
    - type: path
      name: Config
      target: /config
      default: /mnt/user/appdata/valheim
      mode: rw
      required: true
      description: World, BepInEx, Thunderstore cache, and backups.
    - type: port
      name: Game
      target: "2456"
      default: "2456"
      proto: udp
    - type: port
      name: Query
      target: "2457"
      default: "2457"
      proto: udp
      description: Steam A2S. The game port plus one.
    - type: port
      name: Crossplay
      target: "2458"
      default: "2458"
      proto: udp
    - type: port
      name: RCON
      target: "2459"
      default: "2459"
      proto: tcp
      description: Tristan-ValheimRcon. Not Valheim's UDP 2458.
    - type: variable
      name: Server name
      target: SERVER_NAME
      default: Valheim
    - type: variable
      name: World name
      target: WORLD_NAME
      default: Dedicated
      required: true
      description: Filename in worlds_local, without .db or .fwl.
    - type: variable
      name: Server password
      target: SERVER_PASS
      default: ""
      mask: true
      required: true
      description: At least 5 characters. Valheim rejects a shorter one.
    - type: variable
      name: Public
      target: SERVER_PUBLIC
      default: "true"
      description: true lists the server in the browser. false does not.
    - type: variable
      name: Crossplay
      target: CROSSPLAY
      default: "false"
      description: true allows PlayFab clients and uses UDP 2458.
    - type: variable
      name: Backups
      target: BACKUPS
      default: "true"
      description: Hourly zip of worlds_local into /config/backups.
    - type: variable
      name: Admins
      target: ADMINLIST_IDS
      default: ""
      description: SteamID64 values, separated by spaces. Blank leaves adminlist.txt alone.
    - type: variable
      name: Banned
      target: BANNEDLIST_IDS
      default: ""
      description: SteamID64 values, separated by spaces. Blank leaves bannedlist.txt alone.
    - type: variable
      name: Permitted
      target: PERMITTEDLIST_IDS
      default: ""
      description: SteamID64 values, separated by spaces. Blank leaves permittedlist.txt alone.
    - type: variable
      name: Thunderstore profile
      target: THUNDERSTORE_PROFILE
      default: ""
      required: true
      description: The r2modman or Thunderstore Mod Manager share code.
    - type: variable
      name: RCON password
      target: RCON_PASSWORD
      default: ""
      mask: true
      required: true
    - type: variable
      name: BepInEx
      target: BEPINEX
      default: "true"
      display: hidden
    - type: variable
      name: ValheimPlus
      target: VALHEIM_PLUS
      default: "false"
      display: hidden
    - type: variable
      name: Mod sync
      target: PRE_SERVER_RUN_HOOK
      default: /config/feldcore/thunderstore-sync
      display: hidden
game:
  query:
    type: a2s
    port: 2457
  rcon:
    type: source
    port: 2459
    passwordVariable: RCON_PASSWORD
  saves:
    - name: World
      path: /config/worlds_local
      hostDefault: /mnt/user/appdata/valheim/worlds_local
  updates:
    mode: image
```

Game profile kept as the log-reader example. It is not part of the starter list. Player count comes from the server log, and the RCON password is a masked variable:

```yaml
apiVersion: feldcore.store/v1
kind: game
id: minecraft-java
name: Minecraft Java
version: 1.0.0
summary: A Java dedicated server with the player count read from the log, and the world kept in a share.
icon: icons/minecraft.png
categories:
  - Games
container:
  image: docker.io/itzg/minecraft-server:java21
  network: bridge
  requires: Accept the Minecraft EULA on the form. The world is stored in the share you pick.
  configs:
    - type: path
      name: Data
      target: /data
      default: /mnt/user/appdata/minecraft
      mode: rw
      required: true
    - type: port
      name: Game
      target: "25565"
      default: "25565"
      proto: tcp
    - type: variable
      name: EULA
      target: EULA
      default: ""
      required: true
      description: Set to true once you have accepted Mojang's EULA.
    - type: variable
      name: RCON password
      target: RCON_PASSWORD
      default: ""
      mask: true
      display: always
  restart: unless-stopped
game:
  query:
    type: log
    path: /data/logs/latest.log
    pattern: "There are (\\d+) of a max of (\\d+) players online"
    onlineGroup: 1
    maxGroup: 2
  rcon:
    type: minecraft
    port: 25575
    passwordVariable: RCON_PASSWORD
  saves:
    - name: World
      path: /data/world
      hostDefault: /mnt/user/appdata/minecraft/world
  updates:
    mode: image
```

The same `query` object with `"type": "a2s"` and `"port": 27015` is a Steam dedicated server. `feedbuild` then requires `port` and does not require a log pattern. The Games page uses that one object to decide which reader to run.

VM, Ubuntu cloud-init:

```yaml
apiVersion: feldcore.store/v1
kind: vm
id: ubuntu-2404
name: Ubuntu 24.04
version: 1.0.0
summary: Ubuntu Server from the official cloud image, with a user and an SSH key asked for at install.
icon: icons/ubuntu.png
categories:
  - VMs
project: https://ubuntu.com/download/server
vm:
  os: linux
  media: cloud-image
  firmware: uefi
  machine: q35
  cpu: host
  vcpus: 2
  ramMiB: 2048
  diskGiB: 20
  diskBus: virtio
  nic: virtio
  download:
    url: https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-amd64.img
    sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    filename: ubuntu-24.04-server-cloudimg-amd64.img
  cloudInit:
    userData: |
      #cloud-config
      package_update: true
      ssh_pwauth: false
    growPart: true
    ask:
      - username
      - sshPublicKey
  notes:
    - The image is downloaded once, checked, and kept in the isos share.
```

VM, Windows 11, bring your own ISO. There is no Microsoft URL in the template. The driver disk is virtio-win:

```yaml
apiVersion: feldcore.store/v1
kind: vm
id: windows-11
name: Windows 11
version: 1.0.0
summary: Windows 11 on UEFI with a TPM. You provide the ISO. Virtio drivers are offered from the Fedora driver disk.
icon: icons/windows.png
categories:
  - VMs
vm:
  os: windows
  media: iso
  firmware: uefi
  machine: q35
  cpu: host
  vcpus: 4
  ramMiB: 8192
  diskGiB: 64
  diskBus: virtio
  nic: virtio
  tpm: true
  secureBoot: true
  iso:
    required: true
    share: isos
    filenameHint: Windows 11 installer
  driverDisk:
    url: https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/stable-virtio/virtio-win.iso
    sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    filename: virtio-win.iso
  notes:
    - FELDCORE does not include Windows. Put an ISO you already have in the isos share.
```

Older Windows profiles are the same document with the hardware row from the table above (`firmware: bios`, `machine: pc`, `diskBus: ide`, an era-correct `nic`) and no `driverDisk` until the guest can use virtio. macOS, if added, is this BYO shape with `os: macos` and no `download`.

Bundle. Member ids are other templates in the same feed:

```yaml
apiVersion: feldcore.store/v1
kind: bundle
id: media-stack
name: Media stack
version: 1.0.0
summary: Prowlarr and Sonarr on one network, with the indexer URL filled in and one question for the download folder.
icon: icons/media-stack.png
categories:
  - Media
bundle:
  network: media
  members:
    - id: prowlarr
      alias: prowlarr
    - id: sonarr
      alias: sonarr
  links:
    - from: prowlarr
      to: sonarr
      variable: PROWLARR_URL
      value: http://prowlarr:9696
  shared:
    - id: downloads
      name: Downloads folder
      type: path
      default: /mnt/user/downloads
      description: Both apps mount this folder.
      members:
        - alias: prowlarr
          target: /downloads
        - alias: sonarr
          target: /downloads
```

## 3. Build pipeline

### feedbuild

A small Go program in the store repo, `feedbuild`, is the publisher. YAML parsing is its only library dependency. Game patterns are compiled with Go's `regexp`, the same engine the daemon uses, so a pattern the build accepts is a pattern the server can run. The schema file is the one in this document.

On every run it:

1. Reads `templates/**/*.yaml`, one document each.
2. Validates each document against `#/$defs/template`.
3. Applies the extra rules listed under the schema (unique ids, icon bytes, SVG, regex, password defaults, Windows and macOS URLs, bundle membership, `replaces` uniqueness against a checked-in sample or a freshly downloaded CA feed).
4. Writes `feed.json` with `schemaVersion`, `generatedAt`, `commit` (the git SHA being built), `entries`, and `assets` (relative icon path to SHA-256).
5. Writes a deterministic `assets.tar.zst` of those icon files (sorted names, epoch mtime, uid 0).
6. Refuses to emit a feed whose JSON exceeds 8MB or whose asset archive exceeds 32MB.

The CA match check needs a copy of `applicationFeed.json`. The Action downloads the public feed during the build. A template that names an image the sample does not contain is allowed (the app may have left CA) and is reported. A template that matches two CA apps fails the build.

`feedbuild` is not part of the FELDCORE daemon. The daemon consumes `feed.json` only. Putting the compiler in the store repo lets the private feed change without a FELDCORE release. When the schema in this document changes, the store repo copies the new schema in the same change as the templates that need it.

### GitHub Action

The workflow lives in the store repo.

- On a pull request, a read-only job runs `feedbuild` and uploads `feed.json` as a workflow artifact. It does not sign and it does not publish.
- On a push to `main`, a second job runs in a GitHub environment named `feed`. That environment holds the secret `FEED_MINISIGN_KEY` and is the only place that can publish. The job runs `feedbuild`, signs, and updates the Release.
- `permissions` on the pull-request job are `contents: read`. The publish job has `contents: write`.

Signing is minisign over the exact bytes of `feed.json`:

```
minisign -S -s feed.key -m feed.json -x feed.json.minisig
```

The secret key is generated on the owner's machine (`minisign -G`), stored as the Actions secret, and kept offline by the owner. `keys/feed.pub` is committed so the fingerprint can be read in the repo. The public key in git is a convenience for that comparison. FELDCORE does not treat "the key we just downloaded from the repo" as authority. The owner pins the key in Settings once.

The publish job moves a single Release named `feed` onto the commit it just built, then uploads three assets, replacing any previous ones:

| Asset | Role |
| --- | --- |
| `feed.json` | The catalogue the daemon parses |
| `feed.json.minisig` | The signature over those bytes |
| `assets.tar.zst` | Icon files named in `feed.json` |

`sha256sum feed.json` is printed in the Release notes for a human. The daemon does not trust that line. The signature already covers the file, and a sibling `.sha256` file would itself be unsigned.

A private Release asset is downloaded with the GitHub API. The store login needs Contents: Read, which both the GitHub App and the fallback fine-grained token have. The Contents API is a poor fit: it base64-encodes one file and limits each file to 1MB, and a browser cannot send the token to `raw.githubusercontent.com`. Release assets avoid both limits. Icons are not hotlinked.

### Icons and other assets

Icons are png, svg, or webp, at most 256KiB, referenced as `icons/...` from the template. The signed feed lists each hash. After the signature check, the daemon unpacks the archive into the source's cache directory, hashes each file, and deletes any file whose hash is not the one in the feed. Extra files in the archive are ignored. A missing icon does not fail the feed; the card falls back to the box placeholder the App Store already uses when an image fails to load.

The browser never sees a GitHub URL for these icons. The catalogue carries a same-origin path, `/api/v1/apps/assets/<source-id>/<icon>`, and the daemon serves the cached file. The route is behind the session, like every other `/api/v1` route. The path is cleaned and must stay inside that source's asset directory. The response sets `X-Content-Type-Options: nosniff` and a content type from the extension. SVG is additionally rejected at build time when it contains a script or an external reference. Community Apps icons stay as the public `http(s)` URLs they are today; those hosts are already on the card.

Screenshots and other assets can use the same archive and the same hash list later. The MVP ships icons only.

## 4. Authentication

### One login for the whole feed

The FELDCORE Store has one sign-in. The owner does it once, in Settings. After it succeeds, every template in the feed installs with no further GitHub prompt: containers, games, bundles, VM profiles, overrides, and SDR apps. A template has no token field. Set up and Apply do not ask for GitHub. Installing a second app does not ask again.

That sign-in is how the daemon reads the private feed. It is not a credential per app, and it is not a prompt the install dialog can raise.

The recommended sign-in is a GitHub App the owner creates on the Herculade0 account, installed only on `Herculade0/feldcore-store`, using device flow:

- Permissions: Contents: Read. Metadata: Read is included. Packages: Read as well, so a private `ghcr.io` image published for that repository is pulled with the same sign-in. The install dialog still asks for nothing.
- Repository access: only the store repository. The App is not installed on any other repo.
- Device flow is enabled. The owner clicks Sign in, the page shows an 8-character code and `https://github.com/login/device`, and the daemon polls `POST https://github.com/login/oauth/access_token` no faster than the interval GitHub returns. The owner never sees the access token and never pastes one.
- The App's client id is public. It is saved once in Settings. It is not a secret, and it is not compiled into the binary, because the App belongs to this account. Device flow does not use the client secret.
- A GitHub App user token expires in about 8 hours and comes with a refresh token that lasts about 6 months. The daemon refreshes it before a download. That refresh is silent. It is not a dialog, and it is not repeated per app. When the refresh token itself expires, Settings asks for the same one-time sign-in again.
- Polling follows GitHub's rules: `authorization_pending` keeps waiting, `slow_down` uses the new interval, `access_denied` and expiry stop and say so.

An OAuth app cannot do this job. Its device flow grants OAuth scopes, and the scope that reads a private repository is `repo`, which is every private repository the account can read. FELDCORE does not request `repo`.

The fallback, if the owner would rather not register an App, is one fine-grained personal access token pasted once into the same Settings card: Contents: Read, repository access limited to `Herculade0/feldcore-store`. That is still one credential for the whole feed. It is the worse option. The owner has to copy a secret, a fine-grained token cannot pull GitHub Packages, and it expires on a date the owner has to remember. Build the device-flow sign-in. Keep the paste field for the fallback.

Community Apps has no login. It stays the public unsigned feed.

The daemon sends the credential as `Authorization: Bearer` to `api.github.com` only. It is never put in a query string, a log line, or the catalogue JSON. A test request is `GET /repos/Herculade0/feldcore-store/releases/tags/feed` plus `GET /user`. Settings then shows "Signed in as Herculade0" and the lock. When GitHub returns `GitHub-Authentication-Token-Expiration`, that time is stored and the row warns as it gets close. A fine-grained token that is still waiting for organization approval can read public resources and will fail on the private Release. The sign-in stays saved and the message says it is not accepted yet. The owner hits Test again after approval. They do not paste it a second time.

### Token storage

The token has to be recoverable, because the daemon must send it to GitHub. It is not stored the way a session is stored (sessions keep a hash only), and it is not stored as plaintext in `state.json`.

- A 32-byte key is created with `crypto/rand` the first time a secret is saved, written to `/var/lib/feldcore/secrets.key`, mode `0600`.
- The token is encrypted with AES-256-GCM. The nonce is random per secret. `state.json` holds `enc:v1:` plus the nonce and ciphertext. `state.json` stays mode `0600`, as it is today.
- A copy of `state.json` by itself cannot be decrypted. A backup that contains both the state file and `secrets.key` can. Exports and any future support bundle omit `secrets.key` and omit ciphertext.
- Losing `secrets.key` makes the ciphertext unreadable. Settings drops it and asks for the one-time sign-in again (or the fallback token, if that was what was saved). There is no key escrow.
- The API that reads settings returns `configured: true`, the GitHub login, the expiry, the repo, and the last status. It does not return the access token, the refresh token, a suffix of either, or the ciphertext. The fallback paste field loads empty. The hint is "Signed in. Sign in again to replace it."
- Sign out deletes the ciphertext and leaves the cached feed in place. The cache contains templates, not the credential.
- Logs and errors from the GitHub client pass through a redactor that strips `Bearer` values and `github_pat_` / `ghp_` / `gho_` / `ghr_` prefixes before they reach `slog`.

The same key and the same write-only rule cover the refresh token and, if used, the fallback personal access token. There is no second secret for installing an app.

### The two catalogues, and the lock

Settings → App Store is the Community Apps switch, the auto-refresh switch, and one store sign-in.

| Catalogue | Auth | Address |
| --- | --- | --- |
| Community Apps, on unless the switch is off | none | The catalogue URL. Blank is the public feed. |
| FELDCORE Store | the one GitHub App sign-in, or the one fallback token | `Herculade0/feldcore-store`, release tag `feed` |

The store is used after Sign in has succeeded and the signing key has been pinned. The card has Sign in, Sign out, Replace signing key, and Test. A later extra catalogue, if one is ever added, would be another source with its own sign-in. It is not part of the MVP, and it would still be one login for that whole feed, not a token per app.

The lock sits on the FELDCORE Store in Settings and on its toggle:

| State | When |
| --- | --- |
| Locked | Signed in, and the last GitHub call returned 200 or 304. |
| Rejected | The last call was 401 or 403. The sign-in is kept. |
| Offline | The last call did not connect, or the signature failed. |
| No lock | Community Apps. That catalogue has no credential. |

The FELDCORE Store status line names the catalogue once ("FELDCORE Store · private · saved 16 Nov 14:02"). Cards do not repeat the lock.

The store repo and the release tag are fixed for this catalogue (`Herculade0/feldcore-store`, tag `feed`). The source id is the slug `store`. It stays put if the GitHub repository is renamed, so container labels do not change.

### Images, including GHCR

Public images pull anonymously, which is what the daemon does today. A private image on `ghcr.io` that the same GitHub App can read is pulled with that same sign-in: Docker's `X-Registry-Auth` header (base64 of the GitHub username, the access token, and `ghcr.io`) is sent on the pull. That header does not exist on the pull path today. The install dialog does not grow a password field for it.

Packages: Read on the App is what makes this the same login. A fine-grained personal access token cannot read GitHub Packages, which is another reason it stays the fallback. There is no classic `read:packages` token and no separate registry list in the MVP. A registry that is not GitHub waits until a template actually needs one.

If a pull fails because the image is private and the App was not granted Packages: Read, the error says so and points at Settings. The owner adds the permission on the App and signs in once more. Apps that are already installed are left as they are.

## 5. How FELDCORE consumes a source

### Cache

```
/var/lib/feldcore/
  ca-feed.json              # Community Apps, unchanged
  ca-feed.time
  secrets.key
  state.json                # ciphertext only, for the one store sign-in
  store/
    <source-id>/
      feed.json             # last verified feed
      feed.time
      etag
      assets/               # unpacked icons that matched the signed hashes
```

Community Apps keeps its current files so a daemon that has not been shown a store source behaves exactly as it does today. Each FELDCORE source has its own directory. Removing a source removes the directory. A failed refresh does not truncate it.

Opening the App Store reads disks. The contract of today's `Catalogue()` call stays: the page does not wait on GitHub.

### Refresh

Refresh walks the enabled sources. One source failing does not skip the others, and does not fail the Community Apps refresh.

For a GitHub source:

1. `GET /repos/{owner}/{repo}/releases/tags/{tag}` with the bearer token and `If-None-Match` when an ETag is saved.
2. 304: touch `feed.time`, clear offline, leave the parsed feed as it is. Signature is not checked again; the bytes did not change.
3. 200: download `feed.json`, `feed.json.minisig`, and `assets.tar.zst` with `Accept: application/octet-stream`. Cap the JSON at 8MB and the archive at 32MB.
4. Verify the signature against the pinned public key. On failure, delete the temp files, keep the cache, set the source to untrusted.
5. Parse `schemaVersion` and entries. On failure, keep the cache and say the feed could not be read.
6. Check asset hashes while unpacking into a temp directory, then rename it into place. Save the new ETag only after that rename.

The first time a source is enabled and has no cache, the App Store page refreshes once, which is what it already does when the CA catalogue is missing.

Refresh has both of these, in the MVP:

- A Refresh button on the App Store page. It refreshes every catalogue that is enabled. Community Apps is skipped when its switch is off. One catalogue failing leaves the other alone.
- An auto-refresh option in Settings, on by default. While it is on, the daemon runs that same refresh every 24 hours, which is the existing stale window. While it is off, the only downloads are the button and the first visit to an empty cache. Turning it off does not delete a saved catalogue.

The 24-hour stale flag is still computed (`staleAfter`) and shown when a catalogue is older than that, which is what the owner sees if auto-refresh is off or the last attempt failed. Auto-refresh fetches the catalogues the server is configured to show. It does not send a telemetry report.

### Offline, and a source that is down

| Result | Cache | What the user sees |
| --- | --- | --- |
| 200, signature good | Replaced | The new merge. |
| 304 | Kept | "Saved …", no warning. |
| Network error, timeout, 5xx | Kept | "FELDCORE store is offline. Showing the saved copy." Other sources, including Community Apps, still merge. |
| 401 | Kept | "GitHub rejected the token." Lock switches to rejected. |
| 403 | Kept | "The token cannot read this release." The note mentions Contents: Read, organization approval, and SSO, which are the three usual causes. |
| 404 | Kept | "There is no release named feed on that repo." |
| Bad signature or bad JSON | Kept | "The new feed was rejected. Showing the saved copy." |
| `schemaVersion` too new | Kept | "This FELDCORE is too old for that feed." |
| No cache and any failure | Empty for that source | That source contributes nothing. CA still shows if it has a cache. The empty-store message names the source. |

A source that has never succeeded does not invent an empty catalogue over the top of Community Apps. The merge simply lacks that source.

Installing from a cached template still needs the network for two things the catalogue does not contain: `docker pull`, and a VM `download`. The dialog does not pretend otherwise. A cached icon, a cached config, and a cached description all work with GitHub unreachable.

### Trust

The pinned minisign public key is the trust root for a FELDCORE source. It is pinned when the source is added: the user pastes the one-line public key, the UI shows the key id, and the user confirms it matches `keys/feed.pub` in the repo (or the offline copy they generated). From then on, a refresh with a bad signature, a missing signature, or a different key is refused. There is no "use it anyway".

Replacing the key is an explicit button, the same shape as replacing the token: paste, confirm the new id, save. A change of `keys/feed.pub` in the repo does not rotate the pin.

What the signature is worth: it stops a Release asset that was modified by someone who can write to the repo but cannot read the Actions secret, and it stops a truncated download. It does not stop a person who can edit the workflow and the secret. TLS protects the call to `api.github.com`. The pin stops FELDCORE from silently following a new key that arrived over that connection.

Community Apps is unsigned. FELDCORE records the SHA-256 of the last CA body it accepted so the UI can say the catalogue changed, and it keeps accepting that feed. Refusing CA for lack of a signature would turn a public file FELDCORE does not control into an outage.

The daemon's GitHub client uses the system trust store and adds no private CA. Release-asset downloads use `Accept: application/octet-stream`. GitHub either streams the bytes (200) or redirects (302). The client follows a redirect only to `release-assets.githubusercontent.com` or `objects.githubusercontent.com`, the CDNs GitHub has used for those assets, and then checks the signature before the new bytes replace the cache. A redirect to any other host is a failed refresh. The signature is what makes a cleartext hop on that CDN safe to follow.

## 6. UI and UX

The App Store page keeps its search box, category dropdown, Deprecated toggle, 48-at-a-time grid, Refresh button, and "Set up" action. Two toggle buttons under the title switch the grid between the catalogues.

### Browsing

The toggles are **Community Apps** and **FELDCORE Store**, the same kind of pressed-state button as Deprecated. Exactly one is selected. They switch the list. They do not filter one mixed grid.

- Community Apps shows that catalogue after overrides and hides. An overridden app keeps its place in this list, wears the FELDCORE badge, and opens the fixed template.
- FELDCORE Store shows only store entries: new apps, overrides, games, bundles, and VMs. Inside this view, pills narrow it further: Apps, Games, Bundles, VMs. SDR is a category in the Apps dropdown, from the template's `categories`. Games are not copied into the Community Apps grid.

Search, category, and Deprecated apply to the catalogue that is selected. A status line under the toggles describes that catalogue, including the lock when FELDCORE Store is selected. A warning banner uses that catalogue's own message, so a dead private repo does not replace the Community Apps offline text.

When the Settings switch for Community Apps is off, the Community Apps button is not shown and the page opens on FELDCORE Store. Refresh still updates every catalogue that remains enabled.

A card is the card that exists today (icon, name, author or image, overview, `Requires`, category pills, Beta, Deprecated). Additions:

- A FELDCORE pill when `origin` is `feldcore`.
- "Replaces the Community Apps template" when `overrides` is set.
- On a game card, the query type ("Player count from the log" or "Player count from Steam") and the save names.
- On a bundle card, the member names.
- On a VM card, the guest and the media line ("Cloud image" or "Needs an ISO you already have").

Search covers name, author, image, overview, and categories, as it does now, and also member names for a bundle.

### Installing a container

Unchanged path. The card calls `GET /api/v1/apps/{id}`. An override id is the Community Apps id, so the client does not care which source won. The response is the same `{app, spec, notes}` the dialog already takes, with `requires` still on `app`. Notes still explain path rewrites. A claimed USB device adds a dropdown of matching serials in the device row; with one free match it is already selected. Apply creates the container and, when the spec came from a FELDCORE template, sets `feldcore.template` and `feldcore.template.version`.

Apply does not ask for GitHub. The store sign-in already covers the feed and, with Packages: Read, a private `ghcr.io` image. A public image pulls anonymously, as it does today.

### Installing a game

The same dialog, with a Game section under the container fields: query (read-only, from the template), RCON port (read-only) and the masked password variable, and the save paths as ordinary path rows so the folder picker still works. After apply, the game shows under FELDCORE Store → Games. Player count is requested from `GET /api/v1/apps/games/status` while that view is open, about every 30 seconds, and only for containers with a `feldcore.template` label the feed still knows. A query that gets no answer shows "No answer" and does not restart anything. Update compares the label with the feed and, on confirm, recreates the container the way Edit already does, including the rollback if the new container does not start.

### Installing a bundle

The bundle dialog is one page:

1. Shared questions first (the media folder, the download folder), using the path picker.
2. Each member collapsed, prefilled, able to be opened and edited.
3. Links shown as read-only rows ("Sonarr will get `PROWLARR_URL=http://prowlarr:9696`").
4. A line that names the Docker network that will be created.
5. Apply. The page then shows the members ticking over in order.

Failure stops the list and leaves the successful members up, with "Remove this attempt" on the ones labelled with this install id. Host paths stay. This matches the current rule that removing a container or a share does not delete the files under it.

Docker's manager does not create a user-defined network today. The bundle phase adds that call. Until it exists, bundle templates can sit in the feed and the UI can show them as "Bundles need a newer FELDCORE" rather than failing halfway through create.

### Installing a VM

This waits on M4: a libvirt domain, a disk, and a console. The feed can carry the templates before that. FELDCORE Store → VMs shows them, and Set up explains that VM install is not on this FELDCORE yet, with the hardware and the media line still visible so the catalogue is useful as documentation.

When M4 is there, Set up is one confirm page, not the long wizard:

- Name, which defaults to the template name.
- vCPU, RAM, and disk size, filled from the template.
- One sentence of hardware ("Windows 98: BIOS, IDE disk, rtl8139"). An Advanced disclosure opens the rest of the wizard for a user who wants to change the NIC.
- Media. A cloud image or FreeDOS shows the filename and the checksum status ("Not downloaded", "Ready", "Checksum does not match"). Apply downloads only when the hash is not already satisfied. Windows and macOS show a picker limited to files the user already has, and Apply stays disabled until one is chosen. The sentence on that picker is "FELDCORE does not include this operating system."
- For cloud-init, username and SSH public key when the template asks.

Apply creates the disk, the seed ISO when there is cloud-init, the domain with the template's firmware, machine, CPU, bus, and NIC, and then starts it. The console is M4's console. A checksum failure happens before any domain exists.

### Settings

The App Store card keeps the Community Apps catalogue URL and its validation. Beside it: the Community Apps switch (on by default; off leaves only the FELDCORE Store), the auto-refresh switch (on by default, every 24 hours), and the one store sign-in with its lock. "Hidden by your store" lists hide entries by name and `hideReason`, with no install button. Signing in again, or replacing the signing key, never shows the previous credential.

## 7. Roadmap, effort, risks, open questions

Each phase is shippable on its own. A later phase does not block browsing the templates from an earlier one. Sizes describe the change: which packages move and how invasive that is.

| Phase | What lands | Depends on | Size | Where the work is |
| --- | --- | --- | --- | --- |
| MVP | One GitHub App sign-in for the whole feed (device flow, installed only on `Herculade0/feldcore-store`), encrypted credential, minisign pin, the two catalogue toggles, the Community Apps switch, manual Refresh and 24-hour auto-refresh, merge, badge, hide list, container install through the current dialog, labels, the store repo with `feedbuild` and the Action, one override and one original container. A pasted fine-grained token is the fallback only. | The App Store (M2b), and the owner registering the GitHub App | Medium | `internal/apps` grows the two catalogues and the merge. `internal/store` gains ciphertext and a state-version bump. Settings gains the switches and the sign-in. The App Store page gains the two toggles. The Docker create path gains two labels. A private `ghcr.io` pull sends `X-Registry-Auth` from that same sign-in. Pull, edit, and rollback otherwise stay as they are. |
| Games | Games pill inside FELDCORE Store, log and A2S readers, save rows, version label compared to the feed, Update via the existing recreate. The first game template is modded Valheim, including the Thunderstore sync script. The FELDCORE RCON console is the phase after this one and uses the `source` settings that template already stores. | MVP | Medium | A status handler and a reader. The dialog gains a Game section. Installing Valheim also writes the sync script onto the config volume. No new container runtime. |
| Bundles | Confirm page, network create, ordered create, remove-this-attempt | MVP | Medium | Docker manager learns to create a network. New dialog. The feed format is already fixed in the MVP, so templates can be written early. |
| SDR | USB inventory, serial claim, dropdown on a device row | MVP | Small | A sysfs read, a claim map in state, one control in the dialog. The five SDR templates are YAML. |
| VMs | Confirm page, cloud-image download with checksum, BYO ISO picker, domain create | M4 | Large | Blocked on libvirt, disks, and a console. Templates can land on the VMs pill earlier and show "not installed yet". Windows era profiles are rows in YAML once the wizard can apply machine, bus, and NIC. |

MVP tests, in the style of the current catalogue tests: merge and override against a fixture feed, a hide, a signature fixture with a test key, a refresh that fails and leaves the previous file in place, and a settings response that does not contain the token that was just saved.

### Risks

- **CA shape.** The parser already tolerates the feed's mixed types. An override matches the image name, which is stabler than a template URL. A wholesale override goes stale when upstream adds a port the owner also needs. The owner updates the YAML. That is the maintenance cost of not writing a patch language.
- **CA outage during `feedbuild`.** The uniqueness check downloads CA. If that download fails, the Action fails closed and does not publish. The previous Release stays. The daemon is unaffected.
- **Signing-key loss.** The offline copy is the recovery. Without it, the owner generates a new key, updates the Action secret and `keys/feed.pub`, and uses Replace signing key on each server. Old cached feeds keep working until that replacement, because they were signed by the key still pinned.
- **Token in a root-owned file.** The daemon runs as root and there is one administrator. Encryption limits the damage of a `state.json` copy. It does not limit the damage of root on the box. That is the same trust the daemon already has.
- **One GitHub App token.** It can read the store repo and, with Packages: Read, packages that App is allowed to see. It cannot read the rest of the account. The 8-hour access token is refreshed silently. Losing the refresh token means one sign-in, not a failure on each app. The fallback fine-grained token cannot pull GHCR at all.
- **Legal exposure if a template grows a URL.** The schema rejects Windows and macOS downloads. `feedbuild` is what actually stops a pull request. Review of the private repo is the owner, which is fine while the repo stays private, and is not fine the moment it is public.
- **Badge confusion.** An override that keeps the CA download count can look like an official CA update. The pill and the "Replaces the Community Apps template" line have to be on the card and on the dialog, not only in Settings.

### Licensing and a public feed

FELDCORE is MIT. The store repo's YAML can be MIT too. Image licences stay with the images; the feed points at them and does not vendor them.

| Media | In the private repo | Downloaded by the daemon |
| --- | --- | --- |
| Ubuntu and Debian cloud images | URL and checksum only | Yes, after the hash matches |
| FreeDOS | URL and checksum only | Yes. FreeDOS is free to redistribute |
| virtio-win | URL and checksum only | Yes. It is the Fedora driver ISO |
| Windows 3.1 through 11 | No ISO, no product key, no Microsoft download URL | No. The user picks a file already on disk |
| macOS | No installer, no IPSW, no OpenCore config, no board id or serial | No |

Apple's licence does not permit macOS to run on non-Apple hardware. A private repository does not change that. The recommendation is to leave macOS out of the repo until the owner makes an explicit decision, and even then to store only a BYO hardware stub with no recipe for producing the guest. The open question is whether that stub belongs in the repo at all.

Windows templates are hardware profiles for a guest the owner has a licence for. They are not a way to obtain Windows. The confirm page says the ISO is not included.

Community Apps text is other people's work. A private override that the owner runs on their own server is a local fix. A public feed should carry the owner's own summary and link to the upstream project, and should not paste CA's overview. CA also has a blacklist; a public FELDCORE feed would need the same kind of refusal list and a way to be contacted.

Making the store public is a different product from this design: review before a template is merged, takedown, trademarks (including Unraid and the Community Apps names), no Microsoft or Apple media, and a decision on whether a public entry is allowed to override a CA template other people will see. None of that is required for the private repo. The schema and the source list are enough to add a public URL later as another source, default off.

### Settled with the owner

- The store repo is on the owner's normal GitHub account, `Herculade0/feldcore-store`, and it stays private.
- One sign-in covers the whole feed. Installing an app does not ask again. The sign-in to build is a GitHub App installed only on that repo, via device flow. A pasted fine-grained token is the fallback. The `repo` scope is not used.
- The App Store has two toggle buttons, Community Apps and FELDCORE Store. Settings can disable Community Apps and leave only the store.
- Refresh is a button on the page and an auto-refresh option in Settings. Auto-refresh defaults to on, every 24 hours.
- The starter catalogue does not have to be games. The suggestion is modded Valheim (the first real template), Calibre-Web Automated, Orthanc, OpenWebRX+, and a media bundle. See [Suggested starter catalogue](#suggested-starter-catalogue).

### Open questions

1. **Who holds the minisign key?** The recommendation is an offline copy plus the Actions secret. Confirm that a passwordless key in the secret store is acceptable, or say if the publish step should stop and wait for a hardware-backed signature instead. Waiting makes the Action unable to publish unattended.
2. **Windows.** Confirm the feed never contains Microsoft ISOs, keys, or download links, and that "put an ISO you already have in the isos share" is the whole of the Windows story.
3. **macOS.** Leave it out entirely, or allow a private BYO stub with no installer and no firmware recipe? The recommendation is to leave it out until you decide in writing.
4. **SDR scope.** Is a USB serial claim enough for OpenWebRX+, readsb/tar1090, rtl_433, AIS-catcher, and SatDump, or do any of them need a SoapySDR device string or an rtl_tcp port as well? The schema can grow an optional string without a version bump. It should not grow it in the abstract.
5. **Shared questions in bundles.** One folder applied to several members is the MVP. Is there a second kind of shared value (a timezone, a VPN port) that the first media stack needs?
6. **If this is ever public.** Who reviews templates, is a public override of a CA app allowed, and what is the contact for takedown? Worth answering before any template is written in a way that assumes a public audience. Not worth building for the private repo.
