import { useId, useState } from "react";
import { Alert, Button, Modal, SelectField, TextField, inputClass } from "../form";
import PathInput from "./PathInput";
import { CONFIG_TYPES, blankConfig, type ConfigItem, type ConfigType } from "./types";

/**
 * Add or edit one container setting, in the manner of Unraid's "Add
 * Configuration": choose a type, then fill in the fields that type needs.
 */
export default function ConfigDialog({
  initial,
  onClose,
  onSave,
}: {
  /** The setting being edited, or undefined to add a new one. */
  initial?: ConfigItem;
  onClose: () => void;
  onSave: (item: ConfigItem) => void;
}) {
  const [item, setItem] = useState<ConfigItem>(initial ?? blankConfig("path"));
  const [error, setError] = useState<string | null>(null);
  const descId = useId();
  const adding = !initial;

  const set = (patch: Partial<ConfigItem>) => setItem((cur) => ({ ...cur, ...patch }));

  const changeType = (type: ConfigType) =>
    // Keep the name and description; the other fields mean different things.
    setItem((cur) => ({ ...blankConfig(type), name: cur.name, description: cur.description }));

  const submit = () => {
    const problem = check(item);
    if (problem) {
      setError(problem);
      return;
    }
    onSave(item);
  };

  // Standard access modes, plus whatever unusual one an existing path has.
  const pathModes = ["rw", "ro"];
  if (item.type === "path" && !pathModes.includes(item.mode)) pathModes.push(item.mode);

  return (
    <Modal
      title={adding ? "Add configuration" : "Edit configuration"}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" variant="primary">
            {adding ? "Add" : "Save"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <SelectField
          label="Config type"
          value={item.type}
          onChange={(e) => changeType(e.target.value as ConfigType)}
        >
          {CONFIG_TYPES.map((t) => (
            <option key={t.key} value={t.key}>
              {t.label}
            </option>
          ))}
        </SelectField>

        <TextField
          label="Name"
          value={item.name}
          onChange={(e) => set({ name: e.target.value })}
          placeholder={NAME_HINT[item.type]}
          maxLength={80}
        />

        {item.type === "path" && (
          <>
            <TextField
              label="Container path"
              value={item.container}
              onChange={(e) => set({ container: e.target.value })}
              placeholder="/config"
              hint="Where the folder appears inside the container."
              autoCapitalize="none"
              spellCheck={false}
              className="[&_input]:font-mono [&_input]:!text-xs"
            />
            <PathInput
              label="Host path"
              value={item.host}
              onChange={(host) => set({ host })}
              placeholder="Click to choose a share, or type / to browse"
              hint="The folder on this server to map in."
            />
            <SelectField
              label="Access mode"
              value={item.mode}
              onChange={(e) => set({ mode: e.target.value })}
            >
              {pathModes.map((m) => (
                <option key={m} value={m}>
                  {m === "rw" ? "Read/Write" : m === "ro" ? "Read only" : `Custom (${m})`}
                </option>
              ))}
            </SelectField>
          </>
        )}

        {item.type === "port" && (
          <>
            <div className="grid grid-cols-2 gap-3">
              <TextField
                label="Container port"
                type="number"
                min={1}
                max={65535}
                value={item.containerPort || ""}
                onChange={(e) => set({ containerPort: parseInt(e.target.value, 10) || 0 })}
                placeholder="80"
              />
              <TextField
                label="Host port"
                type="number"
                min={1}
                max={65535}
                value={item.hostPort || ""}
                onChange={(e) => set({ hostPort: parseInt(e.target.value, 10) || 0 })}
                placeholder="8080"
              />
            </div>
            <SelectField
              label="Connection type"
              value={item.proto}
              onChange={(e) => set({ proto: e.target.value })}
            >
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
            </SelectField>
          </>
        )}

        {(item.type === "variable" || item.type === "label") && (
          <>
            <TextField
              label="Key"
              value={item.key}
              onChange={(e) => set({ key: e.target.value })}
              placeholder={item.type === "variable" ? "TZ" : "com.example.role"}
              autoCapitalize="none"
              spellCheck={false}
              className="[&_input]:font-mono [&_input]:!text-xs"
            />
            <TextField
              label="Value"
              type={item.mask ? "password" : "text"}
              value={item.value}
              onChange={(e) => set({ value: e.target.value })}
              placeholder={item.type === "variable" ? "Australia/Brisbane" : ""}
              hint={item.mask ? "This value is masked." : undefined}
              autoComplete={item.mask ? "off" : undefined}
              autoCapitalize="none"
              spellCheck={false}
              className="[&_input]:font-mono [&_input]:!text-xs"
            />
          </>
        )}

        {item.type === "device" && (
          <>
            <TextField
              label="Host device"
              value={item.host}
              onChange={(e) => set({ host: e.target.value })}
              placeholder="/dev/ttyUSB0"
              hint="A device on this server, such as a USB stick or serial adapter."
              autoCapitalize="none"
              spellCheck={false}
              className="[&_input]:font-mono [&_input]:!text-xs"
            />
            <TextField
              label="Container device (optional)"
              value={item.container}
              onChange={(e) => set({ container: e.target.value })}
              placeholder="Same as the host device"
              autoCapitalize="none"
              spellCheck={false}
              className="[&_input]:font-mono [&_input]:!text-xs"
            />
          </>
        )}

        <div>
          <label htmlFor={descId} className="mb-1.5 block text-xs font-bold text-ink-2">
            Description
          </label>
          <textarea
            id={descId}
            value={item.description}
            onChange={(e) => set({ description: e.target.value })}
            rows={2}
            maxLength={400}
            placeholder="Optional note about what this is for"
            className={`${inputClass} resize-y`}
          />
        </div>

        {error && <Alert>{error}</Alert>}
      </div>
    </Modal>
  );
}

const NAME_HINT: Record<ConfigType, string> = {
  path: "e.g. Media library",
  port: "e.g. Web UI",
  variable: "e.g. Time zone",
  label: "Optional",
  device: "e.g. Zigbee stick",
};

/** Catches the obvious gaps here; the server does the full validation. */
function check(c: ConfigItem): string | null {
  switch (c.type) {
    case "path":
      if (!c.container.trim()) return "Enter the container path.";
      if (!c.host.trim()) return "Enter the host path.";
      if (!c.container.trim().startsWith("/")) return "The container path must start with /.";
      return null;
    case "port":
      if (!c.containerPort) return "Enter the container port.";
      if (!c.hostPort) return "Enter the host port.";
      return null;
    case "variable":
    case "label":
      if (!c.key.trim()) return "Enter the key.";
      return null;
    case "device":
      if (!c.host.trim().startsWith("/dev/")) return "A device is a path under /dev.";
      return null;
  }
}
