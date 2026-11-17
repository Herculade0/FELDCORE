---
id: run-docker
kind: guide
title: Run apps with Docker
summary: Install Docker, start a container yourself, or pick one from the App Store.
icon: container
order: 30
popular: true
related:
  - expose-site
  - connect-vpn
references:
  - docker
  - app-store
  - networks
  - snapshots
---

A container is how almost every app runs here. FELDCORE talks to the Docker engine on this machine. It does not run a second copy of Docker.

## If Docker is not running

The Docker page says so, and offers to install Debian's `docker.io` package and start it. That needs root, and it needs `apt`. If you already installed Docker's own packages, FELDCORE uses those and you can ignore the button.

[[route:docker|Open Docker]]

## From the App Store

1. Search or browse the catalogue. It is the same Community Applications feed Unraid uses.
2. Choose an app. The container form opens filled in from the template.
3. Read the notes. Unraid paths are rewritten onto this server's app-data folder. A template that names its own Docker network is placed on **bridge** instead, and the note says so. You can change the network before you apply.
4. Apply. Nothing is installed until you do.

[[route:apps|Open the App Store]]

> [!TIP]
> Leave **Privileged** off unless the app needs it. Privileged gives the container full access to this server. There is no separate scanner that blocks a risky template for you. The check is reading that form.

## By hand

**Run container** asks for an image, a name, a network, ports, folders, and environment variables. The path field offers your shares first. Type `/` to browse real folders on the server.

**Use the GPU** shares `/dev/dri` with the container when an Intel or AMD GPU can be used. NVIDIA is detected and marked not supported yet. Details are in [[help:gpu|GPU]].

## Keep a way back

FELDCORE takes a snapshot before it updates or recreates a container. You can also take one yourself from the container's **Snapshots** button. If that automatic snapshot fails, the edit does not go ahead.

[[route:settings.snapshots|Open snapshot retention]]
