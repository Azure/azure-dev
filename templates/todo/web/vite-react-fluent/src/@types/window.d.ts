/// <reference types="vite/client" />

interface ImportMetaEnv {
    readonly VITE_API_BASE_URL: string;
    readonly VITE_APPLICATIONINSIGHTS_CONNECTION_STRING: string;
}

interface ImportMeta {
    readonly env: ImportMetaEnv
}
