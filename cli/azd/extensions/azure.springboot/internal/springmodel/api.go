// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package springmodel

import (
	"context"
	"io"
)

// AzurePostgreSQLPlugin is the pgJDBC Entra authentication plugin class name.
const AzurePostgreSQLPlugin = azurePostgreSQLPlugin

// ConfigInput describes the source, archive, and runtime inputs for Spring configuration loading.
type ConfigInput struct {
	Root         string
	JarPath      string
	Environment  map[string]string
	Arguments    []string
	JVM          []string
	Deployed     bool
	AllowPending bool
}

// Dependencies summarizes the database-related dependencies discovered for a Spring service.
type Dependencies struct {
	Database      bool
	H2            bool
	PostgreSQL    bool
	AzureIdentity bool
	AzureJDBC     bool
}

func (d Dependencies) toInternal() springDependencies {
	return springDependencies{
		database:      d.Database,
		h2:            d.H2,
		postgresql:    d.PostgreSQL,
		azureIdentity: d.AzureIdentity,
		azureJDBC:     d.AzureJDBC,
	}
}

func dependenciesFromInternal(d springDependencies) Dependencies {
	return Dependencies{
		Database:      d.database,
		H2:            d.h2,
		PostgreSQL:    d.postgresql,
		AzureIdentity: d.azureIdentity,
		AzureJDBC:     d.azureJDBC,
	}
}

// Configuration holds the normalized effective Spring Boot configuration for a service.
type Configuration struct {
	inner *springConfiguration
}

// PackagedDependencies returns the dependencies classified from a packaged Spring Boot JAR, when present.
func (c *Configuration) PackagedDependencies() *Dependencies {
	if c == nil || c.inner == nil || c.inner.packaged == nil {
		return nil
	}
	deps := dependenciesFromInternal(*c.inner.packaged)
	return &deps
}

// DiscoveredDatasource represents the effective datasource inferred from Spring configuration and dependencies.
type DiscoveredDatasource struct {
	Engine             DatabaseType
	Authentication     DatabaseAuthentication
	Host               string
	Port               int
	Name               string
	Username           string
	Password           string
	ClientID           string
	ClientIDConfigured bool
	SSLMode            string
	RootCert           string
	Pending            bool
	SelectionPending   bool
}

// LoadConfiguration loads and normalizes effective Spring Boot configuration from source or JAR inputs.
func LoadConfiguration(ctx context.Context, input ConfigInput) (*Configuration, error) {
	cfg, err := loadSpringConfiguration(ctx, springConfigInput{
		root:         input.Root,
		jarPath:      input.JarPath,
		environment:  input.Environment,
		arguments:    input.Arguments,
		jvm:          input.JVM,
		deployed:     input.Deployed,
		allowPending: input.AllowPending,
	})
	if err != nil {
		return nil, err
	}
	return &Configuration{inner: cfg}, nil
}

// ReadConfigurationData reads a bounded Spring configuration or Bicep parameter document.
func ReadConfigurationData(reader io.Reader) ([]byte, error) {
	return readSpringConfigurationData(reader)
}

// ReadDependenciesInProject inspects a Maven module and its contained parent POM hierarchy.
func ReadDependenciesInProject(ctx context.Context, projectRoot, serviceRoot string) (Dependencies, error) {
	deps, err := readSpringDependenciesInProject(ctx, projectRoot, serviceRoot)
	if err != nil {
		return Dependencies{}, err
	}
	return dependenciesFromInternal(deps), nil
}

// ValidateStandardBootstrap rejects recognizable custom Java bootstrap mechanisms during source discovery.
func ValidateStandardBootstrap(ctx context.Context, root string) error {
	return validateStandardSpringBootstrap(ctx, root)
}

// InferDatasource infers the effective Spring datasource from normalized configuration and dependencies.
func InferDatasource(
	cfg *Configuration, dependencies Dependencies, requireFinal bool,
) (*DiscoveredDatasource, error) {
	var raw *springConfiguration
	if cfg != nil {
		raw = cfg.inner
	}
	inferred, err := inferSpringDatasource(raw, dependencies.toInternal(), requireFinal)
	if err != nil {
		return nil, err
	}
	return &DiscoveredDatasource{
		Engine:             inferred.engine,
		Authentication:     inferred.authentication,
		Host:               inferred.host,
		Port:               inferred.port,
		Name:               inferred.name,
		Username:           inferred.username,
		Password:           inferred.password,
		ClientID:           inferred.clientID,
		ClientIDConfigured: inferred.clientIDConfigured,
		SSLMode:            inferred.sslmode,
		RootCert:           inferred.rootCert,
		Pending:            inferred.pending,
		SelectionPending:   inferred.selectionPending,
	}, nil
}
