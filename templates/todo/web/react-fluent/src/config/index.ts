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
        baseUrl: process.env.REACT_APP_API_BASE_URL || window.ENV_CONFIG.REACT_APP_API_BASE_URL || 'http://localhost:3100'
    },
    observability: {
        connectionString: process.env.REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING || window.ENV_CONFIG.REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING || ''
    }
}

export default config;