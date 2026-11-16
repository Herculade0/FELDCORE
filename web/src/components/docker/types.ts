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
  compose: boolean;
  gpu: boolean;
  webUrl: string;
  network: string;
  ips: string[];
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

export type ConfigType = "path" | "port" | "variable" | "label" | "device";

/** One container setting. Which fields matter depends on `type`. */
export interface ConfigItem {
  type: ConfigType;
  name: string;
  description: string;
  host: string;
  container: string;
  mode: string;
  hostPort: number;
  containerPort: number;
  proto: string;
  hostIp: string;
  key: string;
  value: string;
  /** "always", "advanced" or "hidden". Empty means always. */
  display: string;
  required: boolean;
  /** When set, the value is a secret and the UI must not show it. */
  mask: boolean;
}

export interface Spec {
  name: string;
  image: string;
  network: string;
  restartPolicy: string;
  gpu: boolean;
  configs: ConfigItem[];
  autostart: boolean;
  webUi: string;
  icon: string;
  extraParams: string;
  privileged: boolean;
}

export interface SpecInfo {
  spec: Spec;
  editable: boolean;
  reason?: string;
  running: boolean;
}

export interface Gpu {
  vendor: string;
  name: string;
  supported: boolean;
  note?: string;
}

export interface Options {
  networks: string[];
  gpus: Gpu[];
  gpuAvailable: boolean;
}

export const CONFIG_TYPES: { key: ConfigType; label: string }[] = [
  { key: "path", label: "Path" },
  { key: "port", label: "Port" },
  { key: "variable", label: "Variable" },
  { key: "label", label: "Label" },
  { key: "device", label: "Device" },
];

export function blankConfig(type: ConfigType): ConfigItem {
  return {
    type,
    name: "",
    description: "",
    host: "",
    container: "",
    mode: type === "device" ? "rwm" : "rw",
    hostPort: 0,
    containerPort: 0,
    proto: "tcp",
    hostIp: "",
    key: "",
    value: "",
    display: "always",
    required: false,
    mask: false,
  };
}

/** The one-line "what this setting does" shown in the settings list. */
export function configSummary(c: ConfigItem): { left: string; right: string } {
  switch (c.type) {
    case "path":
    case "device":
      return { left: c.host, right: c.container };
    case "port":
      return {
        left: c.hostPort ? String(c.hostPort) : "auto",
        right: `${c.containerPort}/${c.proto}`,
      };
    default:
      return { left: c.key, right: c.mask && c.value ? "••••••" : c.value };
  }
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
