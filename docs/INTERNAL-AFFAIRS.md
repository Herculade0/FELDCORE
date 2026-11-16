# Internal Affairs

Design only. Nothing in this document is built yet. The milestone is [M2e in the roadmap](ROADMAP.md).

Internal Affairs is the cabinet where the owner's agents file case reports: a change they made, an error they hit, or a note about something they looked into. Each report is an ordinary Markdown file on the server. Agents can add a report, and add an update to a report they filed. They cannot change or delete what is already written. The signed-in administrator reads the cabinet in the web UI and is the only one who can redact.

The name is the FELDCORP one: an agent **files with Internal Affairs**. The page should sound like that, and still make sense to someone who has never run a server.

This sits next to the audit log from [agent access, phase 1](AGENTS.md). The audit log is the automatic list of API and MCP calls a token made. Internal Affairs is what an agent writes, in sentences, about those calls. One is a ledger of requests. The other is a folder of reports.

## Threat model

The daemon is trusted. It runs as root and is the only writer.

Agents are not trusted. A token may file a misleading report, try to overwrite someone else's file, or paste a bearer token into the body. The cabinet has to make those attempts fail or become obvious.

Readers on the network are not trusted. Case files describe this server. They must not be a guest-writable share, and they should not be a guest-readable share either.

A person who already has root can rewrite files and the seal together. Internal Affairs does not try to stop that. It does let the administrator notice when the files and the seal disagree, which covers a restored backup, an edit from Obsidian, and a bug.

Filing a report is not a destructive action. It does not go through the phase 2 approval queue. The queue is for changes to containers, VMs, the network, and shells. A report is only Markdown.

## 1. Storage

### Where the files live

A locked share named `internal-affairs`, under the shares root. On a default install that is:

```text
/srv/feldcore/internal-affairs
```

The shares root is the wizard's folder, or `-shares-root` / `FELDCORE_SHARES_ROOT`. The cabinet follows that root the same way `appdata` does. It is not stored in `/var/lib/feldcore/state.json`. Reports would make that file huge, and the atomic JSON rewrite is the wrong tool for a folder of Markdown.

The share name matches the existing share-name rule (`internal-affairs` is 17 characters). The daemon creates it on startup the way it creates `appdata`, `system`, `domains`, and `isos`, and adopts the folder if it is already there.

If a share with that name already exists and is not this locked share, Internal Affairs stays off. It does not take over a folder the owner created. The page says so: "Internal Affairs is off because a share named internal-affairs already exists. Rename that share if you want the cabinet here."

### What "locked" means

The share record gains a `locked` flag. A locked share is visible on the Shares page so it does not look missing, with a badge and the line "Managed in Internal Affairs." The Shares page cannot delete it, rename it, or turn on SMB or NFS. Deleting a normal share removes the folder only when it is empty; a locked share is not offered for deletion at all.

Comment stored on the share: "Internal Affairs case files. FELDCORE writes these."

SMB and NFS start **off**, same as the other default shares. See [Who can read it from the network](#who-can-read-it-from-the-network).

### On-disk layout

Obsidian can open this folder as a vault. The files use YAML frontmatter, `[[wikilinks]]`, and normal folders. FELDCORE does not write a `.obsidian` directory. If Obsidian creates one, the seal check ignores it.

```text
internal-affairs/
  Home.md
  ledger.jsonl
  changes/2026/11/2026-11-16T130400Z-restarted-plex--ia_ab12cd34.md
  incidents/2026/11/...
  notes/2026/11/...
```

`changes`, `incidents`, and `notes` match the three report kinds. Month folders keep a single directory from growing without bound. The daemon picks the path. A caller never sends a path, a filename, or frontmatter.

File names are ASCII: a UTC timestamp, a slug of the title, and the report id. No spaces, `#`, `|`, or `^`, so wikilinks stay intact.

`Home.md` is a contents page the daemon regenerates: a short explanation, then recent reports as wikilinks grouped by agent name. It is not a case file. Regenerating it is its own ledger entry.

```markdown
# Internal Affairs

Case files filed by agents on this server. FELDCORE writes this page.
Add a report from an agent. Read and redact reports in the FELDCORE app.
Do not edit these files by hand.

## Claude Code
- [[changes/2026/11/2026-11-16T130400Z-restarted-plex--ia_ab12cd34|Restarted Plex]]
```

### Permissions on the files

The daemon writes as root. Agents never get a shell, and they do not get a filesystem path they can write.

While the share is not exported:

- directories `0750`, owner root
- files, including `ledger.jsonl`, `0640`, owner root

That keeps other local Unix accounts out. Turning on the reading room (later) is what relaxes the mode so Samba can read. Writes still go through the daemon, not through Samba's `force user`.

### Who can read it from the network

**Not in the first version.** The administrator reads reports in the web UI. The folder is not on SMB or NFS, so a guest on the LAN cannot open it and cannot edit it.

Today's SMB "read only" mode is `guest ok`: anyone on the network can read. That is the wrong default for case files. Today's "private" mode is read-write for the listed share users. There is no private read-only mode yet.

The later **reading room** adds that mode for this share only:

- SMB on, `read only = yes`, `guest ok = no`, `valid users` set to one share user the owner picks
- NFS stays off
- Samba `veto files` hides `.git` and `.obsidian` if those appear
- The Shares page still cannot switch this share to public or to read-write. The switch lives on the Internal Affairs page: "Let one share user read these files." The hint names the consequence: anyone with that password can read every case file

Obsidian then opens the share as a vault. The hint on the page tells the owner to leave the vault read-only. An edit made in Obsidian is not a filing. The seal check flags it.

### Git, later

The first version does not put the cabinet in git. History is the ledger below, which is enough to see that a file changed and does not need a git binary on the server.

A later version can add a bare repo at `/var/lib/feldcore/internal-affairs.git` with the cabinet as its work tree. The share holds a `.git` pointer file, not the objects. Each ledger entry becomes one commit by `FELDCORE Internal Affairs <internal-affairs@localhost>`. The daemon signs those commits with an SSH key it creates at `/var/lib/feldcore/ia_ed25519` (`0600`). The public key is shown on the page. Agents do not get a git remote.

## 2. Permissions

### Scopes

Two new token scopes. The owner opts in on **Settings → Agent access**, the same place as `read`, `containers`, `vms`, and `admin`.

| Scope | What the token can do |
| --- | --- |
| `ia:file` | File a new report. Append an update to a report **this token** filed. |
| `ia:read` | Search, read, and list reports. |

They are separate. A token can have one, the other, or both. The Agent access card should offer both together under the label "Internal Affairs", with the two ticks visible so a narrower token is still possible.

`ia:file` does not imply `ia:read`. The filing call returns the report that was stored, so an agent can see what was saved, including any secret that was scrubbed, without being able to browse the cabinet.

### What `admin` and `read` do not include

`admin` includes `read`, `containers`, and `vms`. It must **not** include `ia:file` or `ia:read`. `read` must not include `ia:read`.

This is the same idea as the shell. Shell is not a scope, and `Allows` rejects it. These two scopes are real, but `Allows` treats them as an exception: an `admin` token matches `admin` and the three scopes it already includes, and does not match `ia:file` or `ia:read`. A new route stays closed until it is classified, which is already the rule in `scopeFor`. The Internal Affairs routes have to be classified explicitly. Left unclassified, they would fall through to `admin`, and an admin token could file or read.

The browser session is the administrator and is not scoped, as it is everywhere else. The session can read the cabinet. It cannot file or append in the first version. It can redact. Redaction is refused to every token, including `admin`, the way the host terminal is.

### Append-only

Enforced in the daemon, not by asking agents to behave.

- **File** creates a new id and a new file. If the file exists, the call fails. Nothing is overwritten.
- **Append** reads the file, checks its bytes against the ledger, then writes the original body unchanged plus a new section at the end. Frontmatter may change only `updated`, `append_count`, `prev`, `hash`, and the copied audit list (new entries may be added). `kind`, `title`, `agent`, `token_id`, `filed`, `severity`, and `target` stay as filed.
- The caller's token id must equal the report's `token_id`. A different token, even one with the same display name, is refused: "You can only add to reports this token filed."
- A revoked or expired token cannot authenticate, so it cannot append. Creating a new token named "Claude Code" does not inherit the old token's reports.
- There is no edit tool and no delete tool on the MCP server or for bearer tokens.

"Own reports" is the token id, which is stable, not the name, which the owner can reuse after a revoke.

Severity is fixed at filing. To escalate, the agent files a new report and links the old one with a wikilink. That keeps the original file an honest record of how serious it looked at the time.

### The administrator

The signed-in session can redact in the first version, because a scrubber will miss some secrets and there has to be a way to remove them.

Redaction:

- Session only.
- The administrator submits the exact text to remove. The daemon replaces each occurrence in the **body** with `[redacted by the administrator on DATE]`. Frontmatter is not searched.
- The ledger records that a redaction happened, who did it (the administrator account), and the file hash before and after. It does not record the removed text, which may be the secret.
- The bytes outside the replaced span stay as they were.

Delete and "withdraw this report" wait. See [phases](#7-phases). Until then, a report that should not be on the timeline can be redacted to a placeholder, and it remains in the ledger.

### Tamper evidence

A hash chain, not git, in the first version.

`ledger.jsonl` is append-only from the daemon's point of view. One JSON object per line:

```json
{"seq":4,"time":"2026-11-16T13:20:00Z","op":"append","id":"ia_ab12cd34","tokenId":"...","tokenName":"Claude Code","path":"changes/2026/11/2026-11-16T130400Z-restarted-plex--ia_ab12cd34.md","sha256":"<sha256 of the file bytes>","prev":"<line hash of the previous ledger line>","line":"<sha256 of this line without the line field>"}
```

`op` is one of `file`, `append`, `redact`, `index` (Home.md rewritten), and later `reseal`, `seal`, `purge`.

`prev` is the previous line's `line` hash. The first line uses an empty `prev`. The daemon also stores the latest `line` hash and `seq` in `state.json` (root-only, `0600`, not inside the share). That stored head is the seal.

Check the seal when the daemon starts and when the Internal Affairs page loads:

1. Recompute each line hash and the chain.
2. Hash each file the ledger names and compare it with the `sha256` on that file's latest line.
3. Compare the chain head with the seal in `state.json`.
4. Any other file under the cabinet, except `.obsidian/` and later `.git`, is unfiled.

If the check fails, filing and append freeze, so a new entry cannot bury the break. The page shows a banner. The administrator can **Reseal**, which writes `op: reseal` with the old expected head and the new head. Reseal is a decision, recorded, not a silent repair.

`chattr +a` is not used. Root can clear it, and it fights the frontmatter update on append. The ledger is the evidence.

## 3. MCP tools and REST

Phase 1 mounts MCP at `/mcp` and rejects any bearer token that lacks `read` (`requireBearer` in `internal/api/api.go`). Internal Affairs needs a different scope, so that gate has to change.

**Change:** a valid bearer token is enough to open `/mcp`. Each tool checks its own scope. Today's tools still require `read`. `admin` still counts as `read` for those tools. A token with only `ia:file` can connect and file, and `system_info` returns a denied, audited error. A token with only `read` can still call the phase 1 tools and cannot file. Tests have to cover both, including the existing "containers token cannot open MCP" case: that token is now allowed to connect, and every current tool still denies it. The documented behavior in `docs/AGENTS.md` ("the token needs the `read` scope") stays true for the existing tools and gets a paragraph for Internal Affairs.

Do this as part of Internal Affairs, not as a drive-by. Phase 2 write tools will need the same per-tool check.

Tool descriptions should say what happens in plain language. The server instructions stop saying that nothing on the server can change. They say the read tools only look, and the Internal Affairs tools only add Markdown reports. There is still no shell.

Annotations: `file_report` and `append_report` are not read-only, and they are not destructive (`DestructiveHint: false`, `OpenWorldHint: false`). The other three are read-only.

The MCP audit middleware records a tool argument named `id`, `name`, or `query` as the audit target. Free text must not use those keys, or a title containing a secret lands in the audit log. `append_report` and `get_report` use `id` for the report id, which is a good target. `file_report` has no id yet; the handler records the new report id as the target after the write. Bodies are never the audit target.

### Tools

| Tool | Scope | What it does |
| --- | --- | --- |
| `file_report` | `ia:file` | Create a report from a template. Returns the id and the stored Markdown. |
| `append_report` | `ia:file` | Add an update to a report this token filed. |
| `search_reports` | `ia:read` | Literal search across titles, agents, targets, and bodies. |
| `get_report` | `ia:read` | One report, by id. |
| `list_recent` | `ia:read` | Newest reports. |

`file_report` arguments:

| Field | Rule |
| --- | --- |
| `kind` | Required. `change`, `incident`, or `note`. |
| `title` | Required. Up to 120 characters. |
| `body` | Optional. Up to 32 KiB. Empty means the template headings only. |
| `severity` | `info`, `low`, `medium`, `high`, `critical`. Default `info` for a change or a note, `medium` for an incident. |
| `targetKind` | Optional. `container`, `vm`, `share`, `setting`, `host`, `other`. |
| `targetName` | Optional. Up to 64 characters. No slashes. A container name, VM name, share name, or setting name. |
| `auditIds` | Optional. Up to 10 ids. Unknown ids are dropped, not an error. Ids filed by another token are dropped. |

`append_report` takes `id`, a required `body` (up to 32 KiB), and optional `auditIds`. The file must stay under 256 KiB after the append.

`search_reports` takes an optional literal `query` (no regular expressions), plus optional `kind`, `severity`, `agent`, and `target`. Limit default 20, max 50. Matching is case-insensitive. The result is id, title, kind, severity, agent, target, time, and a short snippet. It is an in-process scan of the cabinet. A few thousand small files do not need a search engine.

`list_recent` takes `limit` (default 20, max 50) and an optional `kind`.

`get_report` takes `id` and returns the frontmatter fields and the body. Lookup is by id, never by a caller-supplied path.

### REST

Same rules, same scrubber, same ledger. Session cookies can call the reads and the redact. Bearer tokens can call the routes their scopes allow.

| Method and path | Who |
| --- | --- |
| `POST /api/v1/internal-affairs/reports` | `ia:file` |
| `POST /api/v1/internal-affairs/reports/{id}/append` | `ia:file`, and the token owns the report |
| `GET /api/v1/internal-affairs/reports` | `ia:read`, or the session. Query params match `search_reports` and `list_recent`. |
| `GET /api/v1/internal-affairs/reports/{id}` | `ia:read`, or the session |
| `POST /api/v1/internal-affairs/reports/{id}/redact` | Session only |
| `GET /api/v1/internal-affairs` | Session. Seal status, counts, path, caps. |

A token that fails the scope check gets `403`, and the denial is audited, as elsewhere. A missing report is `404`. A broken seal is `409` with the sentence "Internal Affairs is frozen until the administrator reseals the cabinet."

### Limits

There is no general rate limit on agent routes today (login is limited; token calls are not). Internal Affairs gets its own, in memory, keyed by token id. A daemon restart clears it.

| Limit | Default |
| --- | --- |
| File and append | 30 per rolling hour, and 5 per minute |
| Search, get, list | 120 per minute |
| Title | 120 characters |
| One body | 32 KiB |
| One file | 256 KiB |
| Cabinet | 5,000 reports, or 512 MiB, whichever comes first |

Over the rate limit: `429` and "Too many filings. Wait and try again." Over the cabinet cap: `507` is the wrong signal for a beginner; use `409` and "The Internal Affairs cabinet is full. An administrator can raise the cap or remove old reports."

The 512 MiB cap matters when the shares root is on the operating-system disk. The setup wizard already warns that filling that disk can stop the machine booting. The cap is the backstop. The page shows the cap and how full the cabinet is.

Session reads from the UI are not rate limited.

### Secret scrubbing

Scrub the title, the body, and the target name **before** the file is hashed and written. The hash is of the stored bytes. The tool result is the stored report, so the agent sees the redaction.

Replace:

- a bearer secret (`fc_` plus a long base64url run) with `[redacted token]`
- `Authorization: Bearer …` the same way
- a value after `password`, `passwd`, `secret`, `api_key`, `api-key`, `apikey`, `token`, or `private_key`, when the key is followed by `:` or `=`
- a PEM private-key block
- `access_token=` query values

Run until the text stops changing, with a small iteration cap. If anything was removed, set `scrubbed: true` in the frontmatter.

This will miss secrets that are split, encoded, or described in words. The tool description says: "Don't put tokens or passwords in a report. Internal Affairs removes text that looks like one, and it will miss some." The UI shows a "Scrubbed" mark when the flag is set.

The scrubber does not rewrite the audit log. Token calls still record the action and a short target, not the request body.

## 4. Report templates

The agent picks a kind and sends the fields above. The daemon writes the file. Callers cannot submit their own frontmatter, their own timestamp, or their own token name.

### Frontmatter

```yaml
---
id: ia_ab12cd34
kind: change
title: Restarted Plex
agent: Claude Code
token_id: 5c1e...
filed: 2026-11-16T13:04:00Z
updated: 2026-11-16T13:04:00Z
severity: info
target_kind: container
target_name: plex
scrubbed: false
append_count: 0
audit:
  - id: 9f2a...
    time: 2026-11-16T13:03:40Z
    action: POST /docker/containers/plex/restart
    target: plex
    result: ok
prev: ""
hash: sha256:...
tags:
  - change
  - container
---
```

`agent` is the token's name at filing time. `token_id` is the id. Both are copied from the authenticated token, never from the request. `filed` is the daemon's clock, UTC, RFC 3339. `tags` is `kind` plus `target_kind` when that was set, so Obsidian's tag search has something to use. `hash` is the sha256 of the file with the `hash` line blanked, so a reader can recompute it. `prev` is empty on the first write and is the previous file hash after an append or a redaction.

Ids look like `ia_` plus 16 hex characters, the same random id the audit log already uses.

### Kinds

The body the agent sends is placed under the headings. If the body is empty, the headings are the whole report, so a stub can still be filed and appended to.

**Change record** (`kind: change`). Something the agent changed.

```markdown
## What changed

## Why

## What I checked afterwards
```

**Incident** (`kind: incident`). Something went wrong.

```markdown
## What went wrong

## What I was doing

## What I already tried
```

**Investigation note** (`kind: note`). Something the agent looked into and wants on record.

```markdown
## Question

## What I found

## What is still unclear
```

An append does not touch that text. It adds:

```markdown
## Update — 2026-11-16 13:20 UTC

<the new body>
```

The heading is the daemon's clock, so two updates cannot pretend to be the same moment.

### Link to the audit log

Agents cannot read `GET /api/v1/audit`. That route requires `admin`. They also should not have to. On file and on append the daemon copies this token's recent audit events into the report:

- events from this token in the last 15 minutes, newest first, up to 10
- plus any `auditIds` the caller passed that exist and belong to this token

The copy includes id, time, action, target, and result. It is a snapshot. The live audit log keeps the newest 1,000 events and drops anything older than 90 days, so a link that only stored an id would rot. The report keeps the snapshot after the live row is gone.

A small index file next to the ledger, or the ledger itself, maps audit id → report id. The Audit page shows **Case file** on a row when a report cites it. The report page shows the snapshot, and says "This call is no longer in the live audit log" when the id is absent. Audit rows are not rewritten; the index is the link.

Empty window and no ids is fine. The report is filed with an empty `audit` list. The agent may be writing about something it did not do through the API.

Wikilinks between reports are just Markdown the agent writes (`[[notes/2026/11/...]]`). The daemon does not have to resolve them to store the file. The report viewer resolves a wikilink to another report when it can, and otherwise shows it as text.

### What the agent is told

The implementation updates `docs/AGENTS.md`. The short version, also in the MCP instructions:

> After you change something, hit an error, or finish looking into a problem, file a report with `file_report`. To add to a report you already filed, use `append_report` with its id. You cannot edit or delete a report. Do not put tokens or passwords in the body.

The daemon does not auto-file in the first version. A token that never calls `file_report` leaves no case file. The audit log still has the call. Auto-filing from an approved phase 2 action is a later hook: the daemon would file a change record in that token's name, with the audit id of the action that was applied.

## 5. UI

A new sidebar item, after Audit.

| | |
| --- | --- |
| Label | Internal Affairs |
| Description | Agent reports |
| Icon | A lock or shield from the icon set already used in the sidebar |

Audit stays "Agent calls": the requests. Internal Affairs is the write-up. The empty states should say that in one line so the two pages are not confused.

### Page

Same layout language as the Audit page: a title, one sentence, a card, rows.

**Title:** Internal Affairs

**Subtitle:** Reports your agents file when they change something, hit an error, or look into a problem. Each report is a Markdown file on this server. Agents can add a report. They cannot change or delete what was already filed.

**Empty:** No case files yet. When an agent with an Internal Affairs token files a report, it shows up here. Signing in and clicking around does not.

**Seal ok:** The cabinet matches the Internal Affairs seal.

**Seal broken:** A report was changed outside Internal Affairs. Filing is paused. Review the list, then reseal the cabinet if you accept the files as they are now.

**Filters:** agent (the token name), severity, kind (Change, Incident, Note), and a target box. Filters apply to the timeline. They do not scan the disk from the browser; they call the list endpoint.

**Timeline:** time, title, agent, target, a severity pill. Click a row to open it. Newest first.

**Viewer:**

- Labeled rows, not a dump of YAML: Filed by, When, About, Severity, Kind, Scrubbed.
- The audit snapshot as a short list. Each row links to Audit when the live event still exists.
- The body rendered as Markdown with raw HTML disabled. A report is untrusted text; it must not become a script in the administrator's browser.
- Wikilinks that match a known report open that report. Others stay text.

**Redact:** a session-only control on the open report. It asks for the exact text and shows the replacement sentence before saving. The timeline does not grow a second card; the same report updates, and the viewer notes that the administrator redacted it.

**Obsidian hint,** under the timeline, in ordinary language:

> The files are in `/srv/feldcore/internal-affairs` (use the real path). You can open that folder as an Obsidian vault. Leave the vault read-only. An edit in Obsidian is not a filing, and Internal Affairs will mark the cabinet as tampered and pause new reports.

While sharing is off, the hint says the folder is on this server and is not shared on the network. It does not tell the owner to turn on guest access.

**Retention card:** how many reports, how much space, the cap, and "Reports are kept until you remove them. Automatic deletion is off." The cap is shown even though editing it can wait. Cloud backup is a line of status: "Cloud backup is not set up." See the next section.

The page is behind the normal sign-in. There is no anonymous view.

### Copy on Agent access

Next to the new ticks:

- **File with Internal Affairs** — Create reports, and add updates to its own. Cannot change or delete a report.
- **Read Internal Affairs** — Search and read reports that agents have filed.

The `admin` hint stays accurate: it does not include these two, and the hint should say that in the same breath as the shell, once the shell line is touched. "Includes the scopes above" would become a lie if these ticks sit above it, so place them **below** Admin and don't let the Admin sentence claim them.

## 6. Retention, snapshots, and Proton Drive

### Retention

Default: keep every report until an administrator removes it. Do not copy the audit log's 90 days and 1,000 rows. The audit log is the short memory of calls. The cabinet is the long memory of what agents learned. A 90-day cabinet would throw away the incident an agent is supposed to find next time.

The caps in the limits table are safety limits, not a retention policy. Hitting the cap stops new filings. It does not delete old ones.

Automatic expiry waits until a backup has actually run. The settings that will exist later:

- keep forever, or a number of days
- the size cap
- a confirmation when expiry would delete a report that has never been in a successful backup

Expiry, when it exists, writes a ledger entry and withdraws the file. It does not unlink a file in silence. That feature is not in the first version. The page says automatic deletion is off, so the owner is not left wondering which number applies.

### Snapshots

FELDCORE does not take disk snapshots today. The cabinet is a normal directory of files, so a later snapshot of the shares disk (btrfs, ZFS, LVM, or an appliance image of the storage disk) includes it without a special case. Avoid putting live databases or sockets in the folder. `ledger.jsonl` and the Markdown are the whole cabinet.

The seal lives in `state.json`, under the data directory (`/var/lib/feldcore` by default). On the FELDCORE OS plan, that file is on the config partition, and the cabinet is on the storage disk with the other shares. Restoring one without the other is a normal recovery mistake. The seal check is how the page reports it: the files and the seal do not match, filing pauses, the administrator reseals or restores the missing half.

Do not keep report bodies in `state.json` "so they get into the config backup." That backup is for settings. Case files belong with the data shares, and the config backup should keep the seal so a restored data disk can be checked.

### Proton Drive

No Proton Drive client exists in this repo. When one does, Internal Affairs is a folder it can copy. The contract:

- **Source:** the `internal-affairs` directory, including `ledger.jsonl` and `Home.md`.
- **Leave out:** `.obsidian/` (Obsidian's local UI state). A later git object store lives under `/var/lib/feldcore`, not in this copy. If the owner wants off-site history, the job uploads a git bundle the daemon produced, not a live `.git` directory.
- **Direction:** one way, this server to Proton. Do not mount Proton Drive as the live cabinet, and do not two-way sync it. A laptop edit would break append-only and the seal, and a sync conflict would fork a report.
- **When:** on a schedule (daily is enough), not after every filing. The job records the ledger head it uploaded, in `state.json`.
- **Retention there:** longer than whatever local expiry eventually does. Local expiry must not delete a report whose head has not been recorded as uploaded. Until the job exists, that field stays empty and the page says cloud backup is not set up.
- **Secrets:** scrubbing happens at filing time, so the copy should never contain a bearer token the scrubber recognized. Proton's own encryption is the transport. It is not a reason to skip scrubbing.
- **Credential:** a Proton account for the server, stored with the daemon's other secrets, never as a case file and never in the cabinet.

The first version only reserves the status field and the sentence on the page. It does not upload anything.

## 7. Phases

### IA-1 — first version

Ship this before the reading room, git, or Proton.

- Locked `internal-affairs` share, SMB and NFS off, left alone if the name is already taken
- `ia:file` and `ia:read`, excluded from `admin` and from `read`
- Per-tool MCP scope check, and the five tools
- The REST table, including session read and session redact
- Three templates, daemon-written frontmatter, append-only body
- Scrubber, rate limits, size caps
- Ledger, seal in `state.json`, freeze and reseal
- Audit snapshot and a Case file link on the Audit page
- Internal Affairs page: timeline, filters, viewer, Obsidian hint, retention card that says deletion is off
- `docs/AGENTS.md` updated so an agent knows it should file
- Tests: another token cannot append; a second file cannot overwrite; body bytes survive an append; scrubbed token is not on disk; `read` cannot file; `ia:file` cannot list containers; a flipped byte breaks the seal and blocks filing; a path in `targetName` is rejected; redact is refused to a bearer token

### IA-2 — reading room and withdrawal

- Private read-only SMB for one share user, switch on the Internal Affairs page
- Withdraw a report: move it out of the timeline, keep the bytes, ledger `op: seal`
- Administrator appends a note that is clearly marked as the administrator's, not the agent's
- Optional day cap, still refusing to delete a report that has never been backed up once a backup head exists
- Editable cabinet cap

### IA-3 — history and backup

- Git commits for each ledger entry, SSH-signed, public key on the page
- The snapshot mismatch case described above, which falls out of the seal check once restores happen
- Proton Drive one-way job, honoring this contract
- When a phase 2 approval is applied, file a change record for that token with the audit id attached

### Explicitly not this feature

- A shell, a file browser, or a way to edit a container
- A second audit log of host changes (that item is already on the roadmap, separate from token-call audit)
- Two-way sync with Obsidian or with Proton
- Trusting the prose. The seal shows the file was not altered. It does not show the agent was right.

## Risks

- **The scrubber is a filter, not a guarantee.** A token pasted in pieces, or a password written out in a sentence, will be stored, backed up, and readable by anyone who later gets the reading room. Redaction is the remedy. It is manual.
- **Guest SMB is one checkbox away on every other share, and must not be on this one.** The locked flag has to be enforced in the share manager, not only hidden in the UI.
- **An admin token is powerful.** If `Allows` is left so that `admin` includes the new scopes, the opt-in is meaningless. The exception needs a test next to the shell test.
- **The phase 1 MCP gate change** lets a token without `read` complete the MCP handshake. Existing tools must still deny it. Missing that test would widen phase 1.
- **Audit rows expire.** Anything that only stores an id will dangle. The snapshot in the report is the part that has to survive.
- **The cabinet can fill the OS disk** if the shares root is on it and the cap is raised or skipped.
- **Obsidian and a future two-way sync** will write into the folder and freeze filing. The hint has to say read-only, and the Proton job has to stay one-way.
- **Root ignores the policy.** The seal detects a mismatch after the fact. It is not a control against the daemon's own user.
- **Markdown in the viewer.** Unsanitized HTML in a report is a stored script in the administrator's browser.
- **Clock and names.** The daemon's clock is the timestamp. The token name is a label frozen at filing time; the id is the identity.
- **Home.md rewrites** will look like tampering if the checker hashes every file and forgets that `index` operations are legitimate.

## Open questions

1. **Reading room in the first version?** This design leaves SMB off until IA-2. If you already open vaults from a laptop, private read-only SMB moves into IA-1 and guest access stays unavailable.
2. **Should your existing admin token be able to read the cabinet?** This design says no: tick `ia:read` on the agent that should learn from old reports. An admin token is otherwise the one you would hand to a trusted agent.
3. **Auto-file an incident when a token call returns an error?** This design files only when the agent calls `file_report`, so a noisy container does not fill the cabinet with stubs. The other way is a one-line incident, scrubbed, for each `error` or `denied` result.
4. **Keep forever, locally, until Proton exists?** This design does. If you want a bound sooner, name the period (a year is the shortest that still helps an agent avoid an old mistake).
5. **Git in the first version?** The ledger is enough to detect tampering. Git is for browsing history in a clone. Say if you want that clone before IA-3.
6. **May you file from the page, or only agents?** This design is agents file, you read and redact. A "File a note" button for the signed-in administrator is a small addition if you want your own notes in the same cabinet.
7. **Proton retention, when that backup exists.** How many generations, and is the cabinet in the first backup set or a later one? Local expiry will key off whatever you pick. No client is built here.
8. **Share name.** `internal-affairs` matches the theme and the share-name rule. If you already plan to use that folder for something else, pick the name before IA-1 creates it.
