/// <reference types="vite/client" />

export interface ApiConfig {
    baseUrl: string
}

export interface ObservabilityConfig {
    connectionString: string
}

export interface AppConfig {
    api: ApiConfig
    observability: ObservabilityConfig
}

const config: AppConfig = {
    api: {
        baseUrl: import.meta.env.VITE_API_BASE_URL || 'http://localhost:3100'
    },
    observability: {
        connectionString: import.meta.env.VITE_APPLICATIONINSIGHTS_CONNECTION_STRING || ''
    }
}

export default config;