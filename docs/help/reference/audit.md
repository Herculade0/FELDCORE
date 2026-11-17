---
id: audit
kind: reference
title: Audit log
summary: Token calls, blocked publish requests, and the changes you make on Storage, Stacks, and Backups.
icon: scroll
order: 340
popular: true
page: audit
related:
  - ai-agents
  - secure-your-box
references:
  - api-tokens
---

**Audit** lists two kinds of event in one log.

Agent calls: every request authenticated with a bearer token. The row shows the token name, the action (method and path), the target when there is one, the time, and the result.

Protection blocks: a published request the protection layer refused. The token name is `protection`, the action is `publish.block`, and the result is `denied`. The reason is rate, country, geo-unavailable, bot, empty-ua, or scanner.

[[route:audit|Open the audit log]]

A normal sign-in is not listed, and opening a page is not listed. A recovery-code sign-in is. A change is. Creating a pool, deploying a stack, saving a backup destination, revealing its repository password, changing files in a share, saving health thresholds, and saving notification settings are written here when you do them from the page. The row uses your username. A token call uses the token name. An audit of every Samba or Docker edit FELDCORE makes on its own is not built.

Results are `ok`, `denied`, or `error`. A denied row is a token that failed the scope check, including a shell, or a publish block. An error row is a token call that returned some other 4xx or 5xx.

The newest 1,000 entries are kept. Anything older than 90 days is dropped. **Refresh** reloads the list. It does not export it.

[[route:settings.agent|Open Agent access]]

Internal Affairs, the written reports an agent would file beside this ledger, is [[help:internal-affairs|not in the app yet]].
