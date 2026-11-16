import type { ITheme } from "@xterm/xterm";

/** Cascadia Mono first, then Cascadia Code, then Consolas, then any monospace. */
export const TERM_FONT = '"Cascadia Mono", "Cascadia Code", Consolas, monospace';

/**
 * Windows Terminal Campbell.
 * https://github.com/microsoft/terminal/blob/main/src/cascadia/TerminalApp/themes/Campbell.json
 * Selection is the translucent white Windows Terminal draws over the cells,
 * so coloured text stays readable.
 */
export const campbell: ITheme = {
  background: "#0c0c0c",
  foreground: "#cccccc",
  cursor: "#ffffff",
  cursorAccent: "#0c0c0c",
  selectionBackground: "rgba(255, 255, 255, 0.35)",
  selectionInactiveBackground: "rgba(255, 255, 255, 0.18)",
  black: "#0c0c0c",
  red: "#c50f1f",
  green: "#13a10e",
  yellow: "#c19c00",
  blue: "#0037da",
  magenta: "#881798",
  cyan: "#3a96dd",
  white: "#cccccc",
  brightBlack: "#767676",
  brightRed: "#e74856",
  brightGreen: "#16c60c",
  brightYellow: "#f9f1a5",
  brightBlue: "#3b78ff",
  brightMagenta: "#b4009e",
  brightCyan: "#61d6d6",
  brightWhite: "#f2f2f2",
};

/** Search highlights. Backgrounds stay dark enough that Campbell foreground text remains readable. */
export const searchDecorations = {
  matchBackground: "#5c4a00",
  matchBorder: "#c19c00",
  matchOverviewRuler: "#c19c00",
  activeMatchBackground: "#8a6a00",
  activeMatchBorder: "#f9f1a5",
  activeMatchColorOverviewRuler: "#f9f1a5",
};
