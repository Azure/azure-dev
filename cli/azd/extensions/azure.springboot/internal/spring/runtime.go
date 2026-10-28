// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type environmentStore interface {
	values(context.Context) (string, map[string]string, error)
	set(context.Context, string, string, string) error
}

type grpcEnvironmentStore struct {
	client *azdext.AzdClient
}

func (s *grpcEnvironmentStore) values(ctx context.Context) (string, map[string]string, error) {
	current, err := s.client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", nil, fmt.Errorf("getting current azd environment: %w", err)
	}
	if current.GetEnvironment().GetName() == "" {
		return "", nil, errors.New("no current azd environment is selected")
	}
	name := current.GetEnvironment().GetName()
	response, err := s.client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: name})
	if err != nil {
		return "", nil, fmt.Errorf("getting values for azd environment %q: %w", name, err)
	}
	values := make(map[string]string, len(response.GetKeyValues()))
	for _, pair := range response.GetKeyValues() {
		if pair != nil {
			values[pair.GetKey()] = pair.GetValue()
		}
	}
	return name, values, nil
}

func (s *grpcEnvironmentStore) set(ctx context.Context, envName, key, value string) error {
	_, err := s.client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("setting %s in azd environment %q: %w", key, envName, err)
	}
	return nil
}

// Runtime implements the Spring host's lifecycle preparation.
type Runtime struct {
	environments environmentStore
	deployments  deploymentSafetyChecker
	reconcile    func(context.Context, map[string]string) error
}

// NewRuntime creates a Spring lifecycle runtime backed by azd's stable services.
func NewRuntime(client *azdext.AzdClient) *Runtime {
	return &Runtime{
		environments: &grpcEnvironmentStore{client: client},
		deployments:  newDeploymentSafetyChecker(client),
		reconcile:    newFirewallReconciler(client).Reconcile,
	}
}

func (r *Runtime) resolveService(
	service *azdext.ServiceConfig,
	values map[string]string,
) (*resolvedService, error) {
	if service == nil || service.GetHost() != SpringHost {
		return nil, validationError(
			"invalid_spring_service",
			"the Spring service configuration is missing or has a different host",
			"set the service host to azure.springboot",
		)
	}
	mode, err := resolveDatabaseType(service, values, os.LookupEnv)
	if err != nil {
		return nil, err
	}
	if provisioned := values[provisionedDatabaseTypeEnv]; provisioned != "" &&
		!strings.EqualFold(provisioned, string(mode)) {
		return nil, validationError(
			"database_type_change",
			fmt.Sprintf(
				"service %q selects %q but this environment was provisioned for %q",
				service.GetName(),
				mode,
				provisioned,
			),
			"create a new azd environment to use a different database type",
		)
	}
	return &resolvedService{service: service, mode: mode}, nil
}

func (r *Runtime) resolveProject(
	project *azdext.ProjectConfig,
	values map[string]string,
	validateFiles bool,
) ([]*resolvedService, databaseType, error) {
	services := springServices(project)
	if len(services) == 0 {
		return nil, "", nil
	}

	resolved := make([]*resolvedService, 0, len(services))
	var selected databaseType
	for _, service := range services {
		item, err := r.resolveService(service, values)
		if err != nil {
			return nil, "", err
		}
		if selected != "" && item.mode != selected {
			return nil, "", validationError(
				"conflicting_service_database_types",
				"Spring services in one project resolve to different database types",
				"use one database type for all azure.springboot services in the project",
			)
		}
		selected = item.mode
		if validateFiles {
			if _, _, err := validateDockerfile(project, service); err != nil {
				return nil, "", err
			}
		}
		resolved = append(resolved, item)
	}
	return resolved, selected, nil
}

// ValidateService performs initialization-time validation without side effects.
func (r *Runtime) ValidateService(ctx context.Context, service *azdext.ServiceConfig) error {
	_, values, err := r.environments.values(ctx)
	if err != nil {
		return err
	}
	_, err = r.resolveService(service, values)
	return err
}

// PreProvision validates every Spring service before persisting the selected mode or password.
func (r *Runtime) PreProvision(ctx context.Context, args *azdext.ProjectEventArgs) error {
	if args == nil || args.Project == nil {
		return validationError("missing_project_config", "project configuration is missing", "check azure.yaml")
	}
	envName, values, err := r.environments.values(ctx)
	if err != nil {
		return err
	}
	services, mode, err := r.resolveProject(args.Project, values, true)
	if err != nil || len(services) == 0 {
		return err
	}

	var password string
	var persistPassword bool
	switch mode {
	case databaseH2:
		if _, present := values[databasePasswordEnv]; present {
			return validationError(
				"password_not_supported_for_h2",
				fmt.Sprintf("%s cannot be set when the database type is h2", databasePasswordEnv),
				"remove the PostgreSQL password or use a new H2 environment",
			)
		}
		if _, present := os.LookupEnv(databasePasswordEnv); present {
			return validationError(
				"password_not_supported_for_h2",
				fmt.Sprintf("%s cannot be supplied when the database type is h2", databasePasswordEnv),
				"remove the PostgreSQL password or select postgresql",
			)
		}
	case databasePostgreSQL:
		password, persistPassword, err = resolvePostgreSQLPassword(values, os.LookupEnv, func() error {
			return r.deployments.EnsureCanGenerate(ctx, envName, values)
		})
		if err != nil {
			return err
		}
	}

	if values[databaseTypeEnv] != string(mode) {
		if err := r.environments.set(ctx, envName, databaseTypeEnv, string(mode)); err != nil {
			return err
		}
		values[databaseTypeEnv] = string(mode)
	}
	if persistPassword {
		if err := r.environments.set(ctx, envName, databasePasswordEnv, password); err != nil {
			return err
		}
		values[databasePasswordEnv] = password
	}
	return nil
}

// PostProvision binds the environment to its provisioned mode before network reconciliation.
func (r *Runtime) PostProvision(ctx context.Context, args *azdext.ProjectEventArgs) error {
	if args == nil || args.Project == nil {
		return validationError("missing_project_config", "project configuration is missing", "check azure.yaml")
	}
	envName, values, err := r.environments.values(ctx)
	if err != nil {
		return err
	}
	services, mode, err := r.resolveProject(args.Project, values, false)
	if err != nil || len(services) == 0 {
		return err
	}
	if values[provisionedDatabaseTypeEnv] == "" {
		if err := r.environments.set(ctx, envName, provisionedDatabaseTypeEnv, string(mode)); err != nil {
			return err
		}
		values[provisionedDatabaseTypeEnv] = string(mode)
	}
	if mode == databasePostgreSQL {
		return r.reconcile(ctx, values)
	}
	return nil
}

// PreBuild creates the owned Dockerfile before native Docker discovery runs.
func (r *Runtime) PreBuild(ctx context.Context, args *azdext.ServiceEventArgs) error {
	if args == nil || args.Project == nil || args.Service == nil {
		return validationError("missing_service_config", "service build configuration is missing", "check azure.yaml")
	}
	_, values, err := r.environments.values(ctx)
	if err != nil {
		return err
	}
	if _, err := r.resolveService(args.Service, values); err != nil {
		return err
	}
	if _, _, err := validateDockerfile(args.Project, args.Service); err != nil {
		return err
	}
	return ensureDockerfile(args.Project, args.Service)
}

// PreDeploy re-observes ACA egress before each PostgreSQL deployment.
func (r *Runtime) PreDeploy(ctx context.Context, args *azdext.ServiceEventArgs) error {
	return r.reconcileService(ctx, args)
}

// PostDeploy rechecks ACA egress after a successful PostgreSQL deployment.
func (r *Runtime) PostDeploy(ctx context.Context, args *azdext.ServiceEventArgs) error {
	return r.reconcileService(ctx, args)
}

func (r *Runtime) reconcileService(ctx context.Context, args *azdext.ServiceEventArgs) error {
	if args == nil || args.Service == nil {
		return validationError("missing_service_config", "service deployment configuration is missing", "check azure.yaml")
	}
	_, values, err := r.environments.values(ctx)
	if err != nil {
		return err
	}
	resolved, err := r.resolveService(args.Service, values)
	if err != nil {
		return err
	}
	if resolved.mode == databasePostgreSQL {
		return r.reconcile(ctx, values)
	}
	return nil
}
