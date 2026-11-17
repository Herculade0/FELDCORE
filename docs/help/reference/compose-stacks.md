---
id: compose-stacks
kind: reference
title: Compose stacks
summary: Paste, upload, or fetch a Compose file, check it, and deploy it on its own network.
icon: layers
order: 225
popular: true
page: stacks
related:
  - run-docker
references:
  - docker
  - networks
  - backups
---

A stack is a Docker Compose file FELDCORE stores and runs with `docker compose`. The project name is `feldcore-` plus the stack name.

[[route:stacks|Open Stacks]]

If Compose is not installed, you can still write and check a file. **Install Compose** adds the v2 plugin. Deploy, update, stop, and logs wait for that.

## Add a stack

1. Choose **New stack**.
2. Name it with lowercase letters, numbers, and hyphens, at most 31 characters. That name is the folder and, by default, the network.
3. Paste the compose file, upload it, or fetch it from an https Git repository. A `.env` beside a fetched file is shown and is not saved until you deploy.
4. Leave **Private network** on unless you want the shared Docker bridge. A file that already defines networks keeps those networks. They stay private to the project unless one is marked external.
5. Tick **Rewrite Unraid paths** when the file uses `/mnt/user`, `/mnt/user0`, `/mnt/cache`, or `/mnt/diskN`. Those are mapped onto this server's shares. Portainer and Unraid labels are kept.
6. Deploy. The first deploy does not ask for a diff. A later deploy of a changed file shows the diff and waits until you confirm.

The files live in the folder named under the title, with `docker-compose.yml`, an optional `.env`, and a generated `feldcore.override.yml`. Do not edit the override by hand. Backups include this folder when you copy Compose. [[help:backups|Backups]].

Git fetch refuses localhost, link-local, and metadata addresses. A private LAN address is allowed only when Settings has turned on LAN URL downloads.

## The security check

Before deploy and before update, every service is reviewed the same way a single container is:

- privileged mode
- a Docker socket, including one that only appears after `.env` is filled in
- host network, host PID, or host IPC
- high-risk capabilities
- a bind mount of `/`, `/etc`, or `/root`

A high finding blocks the deploy until you choose **Deploy anyway**. A warning does not block. **Apply safer setting** removes that one setting from the file. You can save without deploying and leave the findings in the file.

Update pulls images and recreates containers. It runs the check again. It asks for **Update anyway** when a high finding is still there. It does not ask for a file diff.

## From a container

On the Docker page, **Make a stack** writes a compose file from one container that is not already in a Compose project. Deploying that stack removes the original container. Its volumes and mapped folders stay. A container routed through a VPN is refused. Anything the converter does not understand is listed on the stack, not dropped. GPU sharing adds `/dev/dri`.

Containers from a stack are grouped under that stack on the Docker page. Publish still uses the per-container Publish form.

[[route:docker|Open Docker]]

An agent token needs `stacks` to save, deploy, update, stop, or remove, and `read` to list stacks and follow the job. `admin` includes both.
