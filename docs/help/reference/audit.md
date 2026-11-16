---
id: audit
kind: reference
title: Audit log
summary: Token calls and blocked publish requests, kept for 90 days.
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

Signing in from the browser is not listed. Changes you make by clicking around the UI are not listed either. An audit of FELDCORE's own edits to Samba, Docker, and the rest is not built.

Results are `ok`, `denied`, or `error`. A denied row is a token that failed the scope check, including a shell, or a publish block. An error row is a token call that returned some other 4xx or 5xx.

The newest 1,000 entries are kept. Anything older than 90 days is dropped. **Refresh** reloads the list. It does not export it.

[[route:settings.agent|Open Agent access]]

Internal Affairs, the written reports an agent would file beside this ledger, is [[help:internal-affairs|not in the app yet]].
