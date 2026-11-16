// Mirrors the JSON emitted by internal/docker (Go).

export interface Port {
  host: number;
  container: number;
  proto: string;
}

export interface Container {
  id: string;
  name: string;
  image: string;
  state: string;
  status: string;
  created: number;
  ports: Port[];
  managed: boolean;
  webUrl: string;
  cpuPct: number;
  memUsed: number;
  memLimit: number;
}

export interface Image {
  id: string;
  tag: string;
  size: number;
  created: number;
  inUse: boolean;
}

export interface DockerStatus {
  installed: boolean;
  running: boolean;
  canInstall: boolean;
  version?: string;
  apiVersion?: string;
  message?: string;
}

export interface PortMapping {
  host: number;
  container: number;
  proto: string;
}

export interface VolumeMapping {
  host: string;
  container: string;
  readOnly: boolean;
}

export interface EnvVar {
  key: string;
  value: string;
}

export interface CreateRequest {
  name: string;
  image: string;
  ports: PortMapping[];
  volumes: VolumeMapping[];
  env: EnvVar[];
  restartPolicy: string;
  autostart: boolean;
}

// How each container state should read and colour in the UI.
export const STATE_STYLE: Record<string, { label: string; cls: string; dot: string }> = {
  running: { label: "Running", cls: "text-ok", dot: "bg-ok" },
  restarting: { label: "Restarting", cls: "text-warn", dot: "bg-warn" },
  paused: { label: "Paused", cls: "text-warn", dot: "bg-warn" },
  created: { label: "Created", cls: "text-ink-3", dot: "bg-ink-3" },
  exited: { label: "Stopped", cls: "text-ink-3", dot: "bg-ink-3" },
  dead: { label: "Dead", cls: "text-bad", dot: "bg-bad" },
};

export function stateStyle(state: string) {
  return STATE_STYLE[state] ?? { label: state, cls: "text-ink-3", dot: "bg-ink-3" };
}
