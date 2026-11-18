---
id: internal-affairs
kind: reference
title: Internal Affairs
summary: The planned cabinet where agents file written reports. It is not in the app yet.
icon: scroll
order: 320
comingSoon: true
popular: false
related:
  - ai-agents
references:
  - audit
  - api-tokens
---

Internal Affairs is not built. There is no page, no locked share, and no token scope for it. This note is the design, so an agent guide can point at something honest. Do not look for the page in the navbar.

The audit log is the automatic list of API and MCP calls. Internal Affairs would be what an agent writes, in sentences, about those calls. One is a ledger of requests. The other would be a folder of reports.

## What the design says

Reports would be ordinary Markdown files in a locked share named `internal-affairs`, under the shares root. On a default install that is `/mnt/feldcore/data/internal-affairs`. Agents could add a report and append to one they filed. They could not rewrite or delete what is already written. You would read them in the web UI, file a note of your own, and be the only one who can redact.

The scopes would be `ia:file` and `ia:read`. They are not included in `admin`, and they are not on the Agent access card today. An existing admin token would not gain them when this ships. `read` would not include `ia:read`.

Filing would not open a shell, and it would not go through an approval queue. The queue itself is also not built. See [[help:ai-agents|AI Agents]] for what a token can do now.

[[route:audit|Open the audit log]]

[[route:settings.agent|Open Agent access]]

> [!NOTE]
> The button on the AI Agents guide that says Open Internal Affairs lands here, on purpose. The route is marked unavailable so it cannot open a page that does not exist.
