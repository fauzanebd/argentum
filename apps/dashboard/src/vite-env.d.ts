/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string;
  readonly VITE_WS_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

// Injected by `define` in vite.config.ts. See src/lib/version.ts.
declare const __APP_VERSION__: string;
declare const __BUILD_DATE__: string;
