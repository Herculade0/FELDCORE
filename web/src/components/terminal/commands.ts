/** POSIX single quotes, so a share path with spaces or quotes survives `cd`. */
export function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

export function cdCommand(path: string): string {
  return `cd -- ${shellQuote(path)}`;
}

export const HOST_COMMANDS = {
  dockerPs: "docker ps",
  logs: "journalctl -u feldcored -f --no-pager",
  htop: "if command -v htop >/dev/null 2>&1; then htop; else top; fi",
} as const;
