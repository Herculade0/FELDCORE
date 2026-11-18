---
id: app-store
kind: reference
title: App Store
summary: Search the Community Applications catalogue and apply a template yourself.
icon: store
order: 240
popular: true
page: apps
related:
  - run-docker
references:
  - docker
  - networks
---

The App Store is a cache of the public Unraid Community Applications feed. Each entry already contains the template. FELDCORE does not run Unraid's plugin. It downloads the JSON, and turns the template you pick into the container form.

[[route:apps|Open the App Store]]

## Browse

Search matches the name, author, image, overview, and categories. Deprecated apps are hidden until you show them. **Refresh** downloads the feed again. The first visit refreshes on its own when the cache is empty and the server is online.

Icons and categories come from the feed. FELDCORE also lists its own templates on this page. Proton Bridge is the first, with the purpose AI integration. A separate private catalogue feed, with its own signing and toggles, is not in the app.

## Install

Choose an app. The container form opens with the title "Set up …", the template filled in, and any notes. You review paths, ports, variables, the network, and privileged, then apply.

Unraid paths under `/mnt/user`, `/mnt/cache`, and `/mnt/disk` are rewritten into this server's app-data folder. The WebUI field, such as `http://[IP]:[PORT:2456]/`, is kept so a home-network name can be suggested later. `[IP]` means this server and `[PORT:n]` means the published port.

`--gpus` in extra parameters is kept on the spec and is not applied. The GPU switch is separate. See [[help:gpu|GPU]].

Nothing in the catalogue is installed in the background. There is no "update all" and no notification that a managed container has a newer image. That notification is not built yet.

The feed URL can be changed on Settings → General, on the App Store card, if you need a different copy of the same JSON.

[[route:settings|Open Settings]]
