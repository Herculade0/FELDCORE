import { useEffect, useRef, useState } from "react";
import type RFB from "@novnc/novnc";
import { Keyboard, MonitorPlay } from "lucide-react";
import { Button, Modal } from "../form";

export default function Console({ name, onClose }: { name: string; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const rfb = useRef<RFB | null>(null);
  const [status, setStatus] = useState("Connecting…");

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let dead = false;
    let client: RFB | null = null;
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const url = `${proto}://${location.host}/api/v1/vms/${encodeURIComponent(name)}/console`;
    const onConnect = () => setStatus("Connected");
    const onDisconnect = (ev?: { detail?: { clean?: boolean } }) => {
      setStatus(ev?.detail?.clean ? "Disconnected" : "The console closed. The VM may have stopped.");
    };
    const onSecurity = (ev?: { detail?: { reason?: string; status?: string } }) => {
      setStatus(ev?.detail?.reason || ev?.detail?.status || "The VNC server refused the connection.");
    };
    const onCreds = () => {
      setStatus("This VNC server wants a password. FELDCORE's console expects VNC on localhost without one.");
    };
    // noVNC is large and only needed once a console is open.
    void import("@novnc/novnc").then((mod) => {
      if (dead) return;
      client = new mod.default(el, url);
      client.scaleViewport = true;
      client.resizeSession = true;
      client.background = "#05070d";
      client.addEventListener("connect", onConnect);
      client.addEventListener("disconnect", onDisconnect);
      client.addEventListener("securityfailure", onSecurity);
      client.addEventListener("credentialsrequired", onCreds);
      rfb.current = client;
    });
    return () => {
      dead = true;
      client?.removeEventListener("connect", onConnect);
      client?.removeEventListener("disconnect", onDisconnect);
      client?.removeEventListener("securityfailure", onSecurity);
      client?.removeEventListener("credentialsrequired", onCreds);
      client?.disconnect();
      rfb.current = null;
    };
  }, [name]);

  return (
    <Modal
      title={`Console · ${name}`}
      size="xl"
      onClose={onClose}
      footer={
        <>
          <p className="mr-auto hidden text-xs font-semibold text-ink-3 sm:block">
            <Keyboard size={13} className="mr-1 inline" />
            Click the display, then type. The console is only available while you are signed in.
          </p>
          <Button onClick={() => rfb.current?.sendCtrlAltDel()}>Ctrl+Alt+Del</Button>
          <Button variant="primary" onClick={onClose}>
            Close
          </Button>
        </>
      }
    >
      <div className="overflow-hidden rounded-2xl bg-[#05070d]">
        <div className="flex items-center gap-2 px-3 py-2 text-xs font-bold text-white/70">
          <MonitorPlay size={14} />
          {status}
        </div>
        <div ref={ref} className="h-[62vh] min-h-[280px] w-full" />
      </div>
    </Modal>
  );
}
