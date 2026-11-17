// Release builds set VITE_FELDCORE_VERSION from the git tag, with the
// leading "v" already removed. Local `npm run dev` and `npm run build`
// leave it unset and keep the default.
declare global {
  interface ImportMetaEnv {
    readonly VITE_FELDCORE_VERSION?: string;
  }
  interface ImportMeta {
    readonly env: ImportMetaEnv;
  }
}

export const VERSION: string = import.meta.env.VITE_FELDCORE_VERSION || "0.1.2";
