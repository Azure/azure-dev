// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

// CustomProjectConfig defines project-level configuration for the demo extension
type CustomProjectConfig struct {
	// Demo feature flags for project-level configuration
	EnableColors bool `json:"enableColors,omitempty" jsonschema:"description=Enable color output,default=true"`
	// Maximum number of items to display
	MaxItems int `json:"maxItems,omitempty" jsonschema:"description=Max items to display,minimum=1,maximum=100,default=10"`
	// Project labels for demo purposes
	Labels map[string]string `json:"labels,omitempty" jsonschema:"description=Custom project labels"`
}

// CustomServiceConfig defines service-level configuration for the demo extension
type CustomServiceConfig struct {
	// Demo service endpoint configuration
	Endpoint string `json:"endpoint" jsonschema:"required,description=Service endpoint URL,format=uri"`
	// Port for the demo service
	Port int `json:"port,omitempty" jsonschema:"description=Service port,minimum=1,maximum=65535,default=8080"`
	// Environment for demo deployment
	Environment string `json:"environment,omitempty" jsonschema:"description=Environment,enum=development,enum=staging,enum=production"`
	// Health check configuration
	HealthCheck *HealthCheckConfig `json:"healthCheck,omitempty" jsonschema:"description=Health check configuration"`
}

// HealthCheckConfig defines health check configuration
type HealthCheckConfig struct {
	Enabled  bool   `json:"enabled,omitempty" jsonschema:"description=Enable health checks,default=true"`
	Path     string `json:"path,omitempty" jsonschema:"description=Health check path,default=/health"`
	Interval int    `json:"interval,omitempty" jsonschema:"description=Check interval (sec),minimum=5,maximum=300,default=30"`
}
