---
id: docker
kind: reference
title: Docker containers
summary: Install the engine, run and edit containers, read logs, and open a console.
icon: container
order: 220
popular: true
page: docker
related:
  - run-docker
  - expose-site
references:
  - app-store
  - networks
  - snapshots
  - gpu
---

The Docker page lists every container the engine knows about, with state, a live CPU and memory sample, ports, and whether FELDCORE is managing it. FELDCORE's own tunnel and proxy containers show up here too. Edit those from Remote access and Web addresses, not by recreating them by hand.

[[help:run-docker|Run apps with Docker]] is the short version.

## Install

If the engine is not answering, **Install Docker** runs `apt-get install docker.io` and `systemctl enable --now docker`. It needs root and apt. An engine you installed another way is used as-is once the socket answers.

## Create and edit

**Run container** pulls the image if it is not local, then creates the container. You set the name, image, network, restart policy, paths, ports, variables, labels, and devices. Each of those has a name and a description in the form.

Editing reads the container back into the same form and recreates it. If the new one fails, FELDCORE rolls back to the original. A snapshot is taken first. If that snapshot fails, the edit does not proceed. See [[help:snapshots|Snapshots]].

**Start the container now** applies to a new container. A container routed through a VPN that is down is created and left stopped.

## On each row

**Start, stop, restart, remove.** Remove can force a running container. Removing does not delete folders you mapped in.

**Logs** tails stdout and stderr.

**Console** is a shell inside a running container. It needs your browser session. A token cannot open it.

**Web address** is the LAN name and the public hostname. **Publish**, on the edit form, is the single switch that turns the HTTPS name, certificate, and tunnel hostname on together. Both are described in [[help:expose-site|How to expose your site]].

**Snapshots** lists copies for that container.

## Allocations

The table under the list shows each container's network, address, and host ports, so you can see why a port is taken before you pick it.

## Restart policy

The restart policy on the form is Never, Unless stopped, Always, or On failure. That is Docker's behaviour across daemon restarts. It is not the same as the snapshot taken before an edit.

[[route:docker|Open Docker]]
