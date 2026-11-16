declare module "@novnc/novnc" {
  export default class RFB {
    constructor(target: HTMLElement, url: string);
    scaleViewport: boolean;
    resizeSession: boolean;
    background: string;
    focus(): void;
    sendCtrlAltDel(): void;
    disconnect(): void;
    addEventListener(
      type: string,
      listener: (ev?: { detail?: { clean?: boolean; status?: string; reason?: string } }) => void,
    ): void;
    removeEventListener(
      type: string,
      listener: (ev?: { detail?: { clean?: boolean; status?: string; reason?: string } }) => void,
    ): void;
  }
}
