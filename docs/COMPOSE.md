# Compose stacks

A stack is a Docker Compose file FELDCORE stores, checks, and runs with the Compose v2 CLI (`docker compose`). The Stacks page pastes or uploads `docker-compose.yml` and an optional `.env`, or fetches them from an https Git repository. Deploy, update, stop and remove run as background jobs.

FELDCORE does not link the Docker engine SDK or the `compose-go` library. The daemon shells out to `docker compose` for deploy, update, stop, remove, logs and status, and uses `gopkg.in/yaml.v3` to validate the file, rewrite paths, apply safer edits and build the diff.

## Where the files live

Stacks are stored under the FELDCORE data directory:

```
/var/lib/feldcore/compose/<name>/docker-compose.yml
/var/lib/feldcore/compose/<name>/.env
/var/lib/feldcore/compose/<name>/feldcore.override.yml
/var/lib/feldcore/compose/<name>/stack.json
/var/lib/feldcore/compose/README.txt
```

`<name>` is lowercase letters, numbers and hyphens, at most 31 characters. The Compose project name is `feldcore-<name>`. `feldcore.override.yml` is regenerated on save and deploy. It labels every service `feldcore.managed=true` and `feldcore.stack=<name>`. Do not edit it by hand.

Backups include `<data-dir>/compose` when that directory exists. The archive entry is `compose`. See `SelectSources` in `internal/backup`. The on-disk `README.txt` repeats this path so a restored copy is self-describing. If `FELDCORE_DATA_DIR` is not `/var/lib/feldcore`, the Stacks page shows the real path.

## Network

A stack that does not define its own `networks:` joins `feldcore-stack-<name>`, a bridge created for that stack. Other containers are not attached to it. Turn **Private network** off to use the shared `bridge` instead.

If the file already defines networks, FELDCORE leaves them alone. Compose already keeps those networks private to the project unless a network is `external`.

`network_mode: host` is a high-risk finding. `network_mode: service:…` and `container:…` (a VPN sidecar) are not moved onto the stack bridge.

## Security check

Every service is reviewed with the same rules as a single container (`internal/docker.Review`) before deploy and before update:

- privileged mode
- Docker socket mounts, including a socket path that only appears after `.env` interpolation
- host network, host PID, host IPC
- high-risk capabilities
- bind mounts of `/`, `/etc` and `/root`

Relative bind paths are resolved against the stack directory first, so `../` escapes are checked too. High-severity findings block the deploy until the operator chooses **Deploy anyway**. Warnings do not block. **Apply safer setting** removes that one setting from the file. Saving without deploying is allowed with the findings still in the file.

The first deploy does not ask for a diff. Deploying a changed file returns the diff and waits until the request sets `confirm`. Update pulls images and recreates containers. It runs the check again and asks for **Update anyway** when a high finding is still present. It does not ask for a file diff.

## Jobs

Deploy, update, stop and remove use the install job runner (`internal/install.StartSpec`) so the page can poll progress. The job name is `stack:<name>:deploy|update|stop|remove`. Poll `GET /api/v1/stacks/jobs/{id}` (scope `read`). These jobs are quiet in the generic install audit. The stack action itself is audited as `stack.deploy`, `stack.update`, `stack.stop` or `stack.remove` for the person or token that started it. A refused high-risk deploy is audited as `denied`.

Installing the CLI is a separate apt job named `compose`. It tries `docker-compose-v2`, then `docker-compose-plugin`.

## Git import

`POST /api/v1/stacks/import` clones an https URL with `git`, read-only, shallow, and no hooks or file protocol. Localhost, link-local and metadata addresses are refused. Private LAN addresses are allowed only when Settings has turned on LAN URL downloads. The clone is stored in a temporary `.fetch-*` directory under the compose root and deleted after the file is read. The default path is `docker-compose.yml`, with `compose.yaml`, `docker-compose.yaml` and `compose.yml` as fallbacks. A path you type yourself must exist. A `.env` beside the file is returned and not saved until you deploy.

## Unraid and Portainer

The editor can rewrite `/mnt/user`, `/mnt/user0`, `/mnt/cache` and `/mnt/diskN` onto this server's shares (default `/srv/feldcore/<share>`). A share FELDCORE already has uses that share's real folder. The obsolete `version:` key is accepted and ignored. JSON that is also valid YAML is accepted. Unraid labels are kept.

## Convert a container

On the Docker page, **Make a stack** writes a compose file from one container that is not already part of a Compose project. Deploying that stack removes the original container. Its volumes and mapped folders stay. A container routed through a VPN is refused. Flags the converter understands become compose keys. Anything it does not understand is listed on the stack, not dropped silently. GPU sharing adds `/dev/dri`.

Containers from a stack are grouped under that stack on the Docker page. Other Compose projects are grouped by their project name. A list with no Compose containers stays a flat list. Publish uses the existing per-container Publish form, opened from the service's container id.

## API

All routes are under `/api/v1`. `GET` is the `read` scope. Everything else is the `stacks` scope. `admin` includes both. A token that deploys should also have `read` so it can list stacks and poll the job. The browser session is the administrator and is not checked against scopes.

| Method | Path | What it does |
| --- | --- | --- |
| GET | `/stacks` | List stacks, the on-disk root, and whether `docker compose` works |
| POST | `/stacks/preview` | Validate, optionally rewrite and apply fixes. `valid: false` is HTTP 200 |
| POST | `/stacks` | Save a new stack. `deploy: true` starts it after the check |
| PUT | `/stacks/{name}` | Save a new copy of the files |
| POST | `/stacks/{name}/deploy` | Check, save when a file is sent, then `compose up -d` |
| POST | `/stacks/{name}/update` | Check the saved file, then pull and recreate |
| POST | `/stacks/{name}/stop` | `compose stop` |
| DELETE | `/stacks/{name}?volumes=1` | `compose down` and delete the directory. `volumes=1` also deletes named volumes |
| GET | `/stacks/{name}/logs` | `compose logs`. Optional `service` and `tail` (1–1000, default 200) |
| POST | `/stacks/import` | Fetch a file from Git. Nothing is saved |
| POST | `/stacks/from-container` | Write a stack from one container |
| GET | `/stacks/jobs/{id}` | Job progress |

A deploy that needs a decision is HTTP 409 with `findings`, `diff`, `confirm` and `ack`. Send the same body again with `confirm` and, for a high finding, `deployAnyway`.

State-changing calls are written to the audit log: `stack.create`, `stack.save`, `stack.deploy`, `stack.update`, `stack.stop`, `stack.remove`, `stack.convert`, `stack.import`.
