---
id: citizen-id
kind: reference
title: Citizen ID
summary: Users, groups, and who may open each published app. The administrator sign-in can ask for a code too.
icon: badge
order: 255
popular: true
related:
  - expose-site
  - secure-your-box
references:
  - publish
  - settings
---

Citizen ID is the sign-in in front of published apps. It lives on Settings → Citizen ID. There is no separate Citizen ID item in the navbar.

[[route:settings.citizen|Open Citizen ID]]

The card covers four things: the default sign-in page, your own authenticator and passkeys, groups, other users, and which published apps they may open.

Set a parent domain under Network if you want one sign-in to cover every published app on that domain. Until then, the session cookie stays on this host.

[[route:settings.network|Open Network]]

## Who can open an app

On **Published apps**, each hostname has **Who can open it**:

| Choice | Who gets in |
| --- | --- |
| Anyone | No sign-in. This is the default, so a name you already published stays open until you change it. |
| Signed-in users | Anyone with an account, including you. |
| Specific groups | Members of the groups you tick, and you. An empty group list leaves everyone else out. |
| Administrators only | You. |

Saving applies the proxy. If the proxy cannot reload, the save says so. A gated app served without TLS is refused for a client that is not on the local network.

The Publish form on a container has the same idea, with the labels `public`, `signed-in`, `groups`, and `admins`. Those are stored on the published name and enforced. Group names there are comma-separated. Protection still runs first, then Citizen ID, then the app.

[[route:settings.publishing|Open published apps]]

[[help:publish|Publish and protection]] is the hostname and the rate limits. This page is the accounts.

## Users, groups, and your own sign-in

**Add group** takes a name and a description.

**Users** are accounts other than yours. You set a username, a display name, and a password. You can disable an account, reset its password, reset its authenticator, and choose its groups. Disabling an account or changing its password signs that account out.

**Your sign-in** is the authenticator and passkeys on the administrator account. Once an authenticator is enrolled, the administrator sign-in asks for the code as well. A code works once. Replacing or removing an authenticator or a passkey asks for your password and the current code again. A signed-in session is not enough. Passkeys need localhost or HTTPS. An IP address is refused and the page asks for a hostname.

The **Sign-in page** card is the default title, welcome text, logo, accent, background, footer, and up to four links. A published app can override those on its Publish form. A blank field uses this default. A logo or background is a PNG, JPEG, or WebP of at most 80 KB.

A directory user who signs in gets the App Launcher and their own authenticator and passkeys. They do not get Settings. Your administrator session is still the one that manages users.

> [!NOTE]
> Entra ID, OIDC, and Cloudflare Access are not built. Accounts are the local directory on this server.
