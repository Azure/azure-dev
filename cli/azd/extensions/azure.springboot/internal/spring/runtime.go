// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
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
		values["AZURE_ENV_NAME"] = name
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
	client          *azdext.AzdClient
	environments    environmentStore
	deployments     deploymentSafetyChecker
	identities      developerIdentityResolver
	tenants         userAccessTenantResolver
	outputs         deploymentOutputVerifier
	reconcile       func(context.Context, map[string]string) error
	configureAccess func(context.Context, string, resolvedDatabase, map[string]string) error
	prepareNetwork  func(context.Context, *deploymentBinding) (func(context.Context) error, error)
}

// NewRuntime creates a Spring lifecycle runtime backed by azd's stable services.
func NewRuntime(client *azdext.AzdClient) *Runtime {
	return &Runtime{
		client:          client,
		environments:    &grpcEnvironmentStore{client: client},
		deployments:     newDeploymentSafetyChecker(client),
		identities:      newDeveloperIdentityResolver(client),
		tenants:         newUserAccessTenantResolver(client),
		outputs:         newDeploymentOutputVerifier(client),
		reconcile:       newFirewallReconciler(client).Reconcile,
		configureAccess: newPostgresAccessDispatcher(client),
		prepareNetwork:  newFirewallReconciler(client).PrepareAutomatic,
	}
}

func (r *Runtime) resolveService(
	ctx context.Context,
	project *azdext.ProjectConfig,
	service *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	values map[string]string,
	requireLate bool,
) (*resolvedService, error) {
	if service == nil {
		return nil, validationError(
			"missing_spring_service",
			"the Spring service configuration is missing",
			"check the service configuration in azure.yaml",
		)
	}
	if service.GetHost() != SpringHost {
		return nil, validationError(
			"unsupported_spring_host",
			fmt.Sprintf("service %q uses unsupported Spring host %q", service.GetName(), service.GetHost()),
			fmt.Sprintf("set the service host to %s", SpringHost),
		)
	}
	if err := validateServiceConfiguration(service); err != nil {
		return nil, err
	}
	if err := validateApplicationPackaging(service); err != nil {
		return nil, err
	}
	database, err := r.discoverServiceDatabase(ctx, project, service, serviceContext, values, requireLate)
	if err != nil {
		return nil, err
	}
	if provisioned := values[provisionedDatabaseTypeEnv]; requireLate && provisioned != "" &&
		!strings.EqualFold(provisioned, string(database.Type)) {
		return nil, validationError(
			"database_type_change",
			fmt.Sprintf(
				"service %q selects %q but this environment was provisioned for %q",
				service.GetName(),
				database.Type,
				provisioned,
			),
			"create a new azd environment to use a different database type",
		)
	}
	if database.managedAccess() && !database.ServerDeferred {
		if provisioned := values[provisionedDatabaseAccessEnv]; provisioned != "" &&
			!strings.EqualFold(provisioned, database.accessFingerprint()) {
			return nil, validationError(
				"database_access_change",
				fmt.Sprintf("service %q changes the provisioned PostgreSQL access identity", service.GetName()),
				"create a new azd environment or perform an explicit database access migration",
			)
		}
	}
	return &resolvedService{service: service, mode: database.Type, database: database}, nil
}

func (r *Runtime) resolveProject(
	ctx context.Context,
	project *azdext.ProjectConfig,
	values map[string]string,
	validateFiles bool,
	requireLate bool,
) ([]*resolvedService, resolvedDatabase, error) {
	services := springServices(project)
	if len(services) == 0 {
		return nil, resolvedDatabase{}, nil
	}

	resolved := make([]*resolvedService, 0, len(services))
	var selected resolvedDatabase
	for _, service := range services {
		item, err := r.resolveService(ctx, project, service, nil, values, requireLate)
		if err != nil {
			return nil, resolvedDatabase{}, err
		}
		if len(resolved) > 0 && !compatibleDatabaseSelection(selected, item.database) {
			return nil, resolvedDatabase{}, validationError(
				"conflicting_service_database_configuration",
				"Spring services in one project resolve to incompatible database targets or application identities",
				fmt.Sprintf("use one compatible managed database access configuration for all %s services", SpringHost),
			)
		}
		selected = item.database
		if validateFiles {
			if err := validateSpringBuild(project, service); err != nil {
				return nil, resolvedDatabase{}, err
			}
		}
		resolved = append(resolved, item)
	}
	return resolved, selected, nil
}

func compatibleDatabaseSelection(left, right resolvedDatabase) bool {
	if left.discovered && right.discovered && (left.selectionPending || right.selectionPending) {
		return true
	}
	if left.Type != right.Type || left.Authentication != right.Authentication {
		return false
	}
	if !left.managedAccess() {
		return true
	}
	return strings.EqualFold(left.ServerResourceID, right.ServerResourceID) &&
		left.Name == right.Name &&
		left.Username == right.Username &&
		identityResourceID(left) == identityResourceID(right)
}

func identityResourceID(database resolvedDatabase) string {
	if database.AppIdentity == nil {
		return ""
	}
	return strings.ToLower(database.AppIdentity.ResourceID)
}

// ValidateService performs initialization-time validation without side effects.
func (r *Runtime) ValidateService(
	ctx context.Context, service *azdext.ServiceConfig, projects ...*azdext.ProjectConfig,
) error {
	_, values, err := r.environments.values(ctx)
	if err != nil {
		return err
	}
	var project *azdext.ProjectConfig
	if len(projects) > 0 {
		project = projects[0]
	}
	if project == nil {
		if r.client == nil {
			return configurationError("the project is unavailable for automatic database discovery")
		}
		response, readErr := r.client.Project().Get(ctx, &azdext.EmptyRequest{})
		if readErr != nil {
			return readErr
		}
		project = response.GetProject()
	}
	_, err = r.resolveService(ctx, project, service, nil, values, false)
	return err
}

// PreProvision validates every Spring service before persisting selected database inputs.
func (r *Runtime) PreProvision(ctx context.Context, args *azdext.ProjectEventArgs) error {
	if args == nil || args.Project == nil {
		return validationError("missing_project_config", "project configuration is missing", "check azure.yaml")
	}
	if len(springServices(args.Project)) == 0 {
		return nil
	}
	envName, values, err := r.environments.values(ctx)
	if err != nil {
		return err
	}
	services, database, err := r.resolveProject(ctx, args.Project, values, true, false)
	if err != nil || len(services) == 0 {
		return err
	}
	if r.client != nil {
		if _, err := resolveProjectServiceModules(ctx, r.client, args.Project); err != nil {
			return err
		}
	}
	if err := validateBootstrapClientIPEnvironment(os.LookupEnv); err != nil {
		return err
	}
	preparation := database
	if database.discovered {
		preparation, err = automaticProvisionInputs(ctx, args.Project, database)
		if err != nil {
			return err
		}
	}
	if database.discovered &&
		preparation.Type == databasePostgreSQL &&
		preparation.Authentication == databaseAuthenticationManagedIdentity {
		if err := validatePostgresBootstrapImage(postgresBootstrapImage); err != nil {
			return err
		}
	}

	writes := map[string]string{}
	switch preparation.Type {
	case databaseH2:
		for _, name := range []string{
			databasePasswordEnv,
			databaseAppPasswordEnv,
			databaseAuthenticationEnv,
			databaseNameEnv,
			databaseUsernameEnv,
			databaseAdministratorNameEnv,
		} {
			if environmentValuePresent(values, os.LookupEnv, name) {
				return validationError(
					"postgres_configuration_not_supported_for_h2",
					fmt.Sprintf("%s cannot be supplied when the database type is h2", name),
					"remove PostgreSQL credentials and access bindings or select postgresql",
				)
			}
		}
	case databasePostgreSQL:
		switch preparation.Authentication {
		case databaseAuthenticationLegacy:
			password, persist, resolveErr := resolvePostgreSQLPassword(values, os.LookupEnv, func() error {
				return r.deployments.EnsureCanGenerate(ctx, envName, values)
			})
			if resolveErr != nil {
				return resolveErr
			}
			if environmentValuePresent(values, os.LookupEnv, databaseAppPasswordEnv) {
				return validationError(
					"legacy_database_app_password_unsupported",
					fmt.Sprintf("%s cannot be supplied for legacy PostgreSQL access", databaseAppPasswordEnv),
					"remove the application password or use supported Spring datasource authentication",
				)
			}
			if persist {
				writes[databasePasswordEnv] = password
			}
		case databaseAuthenticationPassword:
			var safetyChecked bool
			var safetyErr error
			ensureCanGenerate := func() error {
				if !safetyChecked {
					safetyErr = r.deployments.EnsureCanGenerate(ctx, envName, values)
					safetyChecked = true
				}
				return safetyErr
			}
			administratorPassword, persistAdministrator, resolveErr := resolvePostgreSQLPassword(
				values,
				os.LookupEnv,
				ensureCanGenerate,
			)
			if resolveErr != nil {
				return resolveErr
			}
			applicationPassword, persistApplication := "", false
			if !database.discovered {
				applicationPassword, persistApplication, resolveErr = resolvePostgreSQLAppPassword(
					values, os.LookupEnv, ensureCanGenerate,
				)
				if resolveErr != nil {
					return resolveErr
				}
			}
			if persistAdministrator {
				writes[databasePasswordEnv] = administratorPassword
			}
			if persistApplication {
				writes[databaseAppPasswordEnv] = applicationPassword
			}
			subscriptionID := strings.TrimSpace(values[azureSubscriptionIDEnv])
			if subscriptionID == "" {
				return validationError(
					"missing_azure_subscription",
					"AZURE_SUBSCRIPTION_ID is required to resolve the database hook access tenant",
					"select the target azd subscription before provisioning",
				)
			}
			userAccessTenantID, resolveErr := r.tenants.Resolve(ctx, subscriptionID)
			if resolveErr != nil {
				return resolveErr
			}
			if existing, present := values[databaseUserAccessTenantIDEnv]; present &&
				!strings.EqualFold(existing, userAccessTenantID) {
				return validationError(
					"conflicting_user_access_tenant",
					fmt.Sprintf("%s conflicts with the selected subscription", databaseUserAccessTenantIDEnv),
					"use the original subscription tenant or create a new azd environment",
				)
			}
			writes[databaseUserAccessTenantIDEnv] = userAccessTenantID
		case databaseAuthenticationManagedIdentity:
			for _, name := range []string{databasePasswordEnv, databaseAppPasswordEnv} {
				if environmentValuePresent(values, os.LookupEnv, name) {
					return validationError(
						"password_not_supported_for_managed_identity",
						fmt.Sprintf("%s cannot be supplied for managedidentity auth", name),
						"remove administrator and application passwords from the selected environment and process",
					)
				}
			}
			administratorOverride, resolveErr := resolveAdministratorInputs(preparation, values)
			if resolveErr != nil {
				return resolveErr
			}
			subscriptionID := strings.TrimSpace(values[azureSubscriptionIDEnv])
			if subscriptionID == "" {
				return validationError(
					"missing_azure_subscription",
					"AZURE_SUBSCRIPTION_ID is required to resolve the developer database administrator",
					"select the target azd subscription before provisioning",
				)
			}
			identity, resolveErr := r.identities.Resolve(
				ctx,
				subscriptionID,
				administratorOverride,
			)
			if resolveErr != nil {
				return resolveErr
			}
			for _, binding := range []struct {
				name          string
				value         string
				caseSensitive bool
			}{
				{databaseAdministratorNameEnv, identity.AdministratorName, true},
				{databaseAdministratorObjectIDEnv, identity.ObjectID, false},
				{databaseUserAccessTenantIDEnv, identity.UserAccessTenantID, false},
				{"DATABASE_ADMIN_NAME", identity.AdministratorName, true},
				{"DATABASE_ADMIN_OBJECT_ID", identity.ObjectID, false},
			} {
				conflicts := func(existing string) bool {
					if binding.caseSensitive {
						return existing != binding.value
					}
					return !strings.EqualFold(existing, binding.value)
				}
				if existing, present := values[binding.name]; present && conflicts(existing) {
					return validationError(
						"conflicting_developer_identity",
						fmt.Sprintf("%s conflicts with the current developer identity", binding.name),
						"use the original developer login or create a new azd environment",
					)
				}
				if existing, present := os.LookupEnv(binding.name); present && conflicts(existing) {
					return validationError(
						"conflicting_developer_identity",
						fmt.Sprintf("%s conflicts with the current developer identity", binding.name),
						"remove the conflicting process override before provisioning",
					)
				}
				writes[binding.name] = binding.value
			}
		}
	}

	if !database.discovered {
		writes[databaseTypeEnv] = string(database.Type)
		if database.managedAccess() {
			writes[databaseAuthenticationEnv] = string(database.Authentication)
			writes[databaseNameEnv] = database.Name
			writes[databaseUsernameEnv] = database.Username
		}
	}
	return persistEnvironmentWrites(ctx, r.environments, envName, values, writes)
}

func resolveAdministratorInputs(database resolvedDatabase, values map[string]string) (string, error) {
	administrator := database.AdministratorName
	objectID := ""
	for _, field := range []struct {
		name string
		isID bool
	}{
		{databaseAdministratorNameEnv, false},
		{"DATABASE_ADMIN_NAME", false},
		{databaseAdministratorObjectIDEnv, true},
		{"DATABASE_ADMIN_OBJECT_ID", true},
	} {
		candidates := []string{}
		if value, present := values[field.name]; present {
			candidates = append(candidates, value)
		}
		if value, present := os.LookupEnv(field.name); present {
			candidates = append(candidates, value)
		}
		for _, candidate := range candidates {
			valid := validAdministratorName(candidate)
			selected := &administrator
			if field.isID {
				valid = databasePrincipalIDPattern.MatchString(candidate)
				selected = &objectID
			}
			if !valid {
				return "", validationError(
					"invalid_database_administrator_input",
					fmt.Sprintf("%s is not a valid administrator identity", field.name),
					"provide the current signed-in developer's administrator name and object ID",
				)
			}
			matches := *selected == candidate
			if field.isID {
				matches = strings.EqualFold(*selected, candidate)
			}
			if *selected != "" && !matches {
				return "", validationError(
					"conflicting_database_administrator_inputs",
					fmt.Sprintf("%s conflicts with another explicit administrator input", field.name),
					"make DATABASE_ADMIN_* and SPRING_DATABASE_ADMINISTRATOR_* identify the same developer",
				)
			}
			*selected = candidate
		}
	}
	return administrator, nil
}

func environmentValuePresent(
	values map[string]string,
	lookupProcess func(string) (string, bool),
	name string,
) bool {
	if _, present := values[name]; present {
		return true
	}
	_, present := lookupProcess(name)
	return present
}

func persistEnvironmentWrites(
	ctx context.Context,
	environments environmentStore,
	envName string,
	values map[string]string,
	writes map[string]string,
) error {
	keys := mapsKeys(writes)
	slices.Sort(keys)
	for _, key := range keys {
		if current, present := values[key]; present && current == writes[key] {
			continue
		}
		if err := environments.set(ctx, envName, key, writes[key]); err != nil {
			return err
		}
		values[key] = writes[key]
	}
	return nil
}

func mapsKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

// PostProvision verifies provisioned resources and reconciles managed database access.
func (r *Runtime) PostProvision(ctx context.Context, args *azdext.ProjectEventArgs) (resultErr error) {
	if args == nil || args.Project == nil {
		return validationError("missing_project_config", "project configuration is missing", "check azure.yaml")
	}
	if len(springServices(args.Project)) == 0 {
		return nil
	}
	envName, values, err := r.environments.values(ctx)
	if err != nil {
		return err
	}
	ctx, values, err = r.bindAutomaticProject(ctx, args.Project, envName, values)
	if err != nil {
		return err
	}
	closeNetwork, err := r.beginAutomaticNetwork(ctx)
	defer func() {
		if cleanupErr := closeNetwork(); cleanupErr != nil && !errors.Is(resultErr, cleanupErr) {
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}()
	if err != nil {
		return err
	}
	services, database, err := r.resolveProject(ctx, args.Project, values, false, true)
	if err != nil || len(services) == 0 {
		return err
	}
	if r.client != nil {
		if _, err := resolveProjectServiceModules(ctx, r.client, args.Project); err != nil {
			return err
		}
	}
	if database.Type != databasePostgreSQL {
		if err := closeNetwork(); err != nil {
			return err
		}
		return persistEnvironmentWrites(ctx, r.environments, envName, values, map[string]string{
			provisionedDatabaseTypeEnv: string(database.Type),
		})
	}
	if !database.managedAccess() {
		if err := r.reconcile(ctx, values); err != nil {
			return err
		}
		if err := closeNetwork(); err != nil {
			return err
		}
		return persistEnvironmentWrites(ctx, r.environments, envName, values, map[string]string{
			provisionedDatabaseTypeEnv: string(database.Type),
		})
	}
	if r.outputs == nil {
		return errors.New("current deployment output verification is unavailable")
	}
	values["AZURE_ENV_NAME"] = envName
	binding, err := r.outputs.Verify(ctx, database, values)
	if err != nil {
		return err
	}
	if binding != nil {
		values = binding.values
		ctx = context.WithValue(ctx, deploymentBindingKey{}, binding)
	}
	if err := validateProvisionedBindings(database, values); err != nil {
		return err
	}
	if r.client != nil || (binding != nil && binding.app != nil) {
		for _, service := range services {
			if err := r.verifyExplicitDatasource(
				ctx, args.Project, service.service, nil, service.database, values,
			); err != nil {
				return err
			}
		}
	}
	switch database.Authentication {
	case databaseAuthenticationPassword:
		database.AdministratorName = strings.TrimSpace(values[postgresAdministratorEnv])
		if !validAdministratorName(database.AdministratorName) {
			return validationError(
				"missing_database_administrator_name",
				"the provisioned PostgreSQL administrator login output is missing or invalid",
				"restore AZURE_POSTGRES_ADMINISTRATOR from the current root deployment output",
			)
		}
	case databaseAuthenticationManagedIdentity:
		database.AdministratorName = strings.TrimSpace(values[databaseAdministratorNameEnv])
		if !validAdministratorName(database.AdministratorName) {
			return validationError(
				"missing_database_administrator_name",
				"the verified developer PostgreSQL administrator login is missing or invalid",
				"run azd provision again while signed in as the configured developer administrator",
			)
		}
		if strings.TrimSpace(values[databaseAdministratorObjectIDEnv]) == "" ||
			strings.TrimSpace(values[databaseUserAccessTenantIDEnv]) == "" {
			return validationError(
				"missing_database_administrator_identity",
				"the verified developer PostgreSQL administrator identity is incomplete",
				"run azd provision again while signed in as the configured developer administrator",
			)
		}
	}
	if database.discovered && database.Authentication == databaseAuthenticationPassword {
		values = maps.Clone(values)
		values[databaseAppPasswordEnv] = database.appPassword
	}
	if err := r.configureAccess(ctx, envName, database, values); err != nil {
		return err
	}
	if err := r.reconcile(ctx, values); err != nil {
		return err
	}
	if err := closeNetwork(); err != nil {
		return err
	}
	writes := map[string]string{
		provisionedDatabaseTypeEnv:   string(database.Type),
		provisionedDatabaseAccessEnv: database.accessFingerprint(),
		databaseAccessConfiguredEnv:  databaseAccessPolicyVersion,
	}
	if database.discovered {
		writes[databaseTypeEnv] = string(database.Type)
		writes[databaseAuthenticationEnv] = string(database.Authentication)
		writes[databaseNameEnv] = database.Name
		writes[databaseUsernameEnv] = database.Username
	}
	if database.AdministratorName != "" {
		writes[databaseAdministratorNameEnv] = database.AdministratorName
	}
	return persistEnvironmentWrites(ctx, r.environments, envName, values, writes)
}

func validateProvisionedBindings(database resolvedDatabase, values map[string]string) error {
	serverID := strings.TrimSpace(values[postgresServerIDEnv])
	if serverID == "" || !strings.EqualFold(serverID, database.ServerResourceID) {
		return validationError(
			"mismatched_postgres_binding",
			"database.serverResourceId does not match the current PostgreSQL root deployment output",
			"restore the current root server output and use the selected serverResourceId expression",
		)
	}
	if output := strings.TrimSpace(values[postgresDatabaseEnv]); output != "" &&
		output != database.Name {
		return validationError(
			"mismatched_database_name",
			"database.databaseName does not match the current PostgreSQL root deployment output",
			"restore the authored database selection from the current deployment",
		)
	}
	if database.Authentication == databaseAuthenticationManagedIdentity {
		if database.AppIdentity == nil {
			return validationError(
				"mismatched_database_app_identity",
				"the discovered Spring application identity is unavailable",
				"restore the current application identity and deployment outputs",
			)
		}
		switch database.AppIdentity.Type {
		case databaseAppIdentityTypeSystemAssigned:
			if principalID := strings.TrimSpace(values[serviceIdentityPrincipalIDEnv]); principalID == "" ||
				!strings.EqualFold(database.AppIdentity.ResourceID, values[containerAppIDEnv]) {
				return validationError(
					"mismatched_database_app_identity",
					"the discovered system-assigned identity does not match the selected service application",
					"restore the current Container App and application principal outputs",
				)
			}
		case databaseAppIdentityTypeUserAssigned:
			identityID := strings.TrimSpace(values[serviceIdentityIDEnv])
			if identityID == "" || !strings.EqualFold(identityID, database.AppIdentity.ResourceID) {
				return validationError(
					"mismatched_database_app_identity",
					"the discovered Spring application identity does not match the selected service identity output",
					"restore the current service identity output and use the selected UAMI resourceId expression",
				)
			}
		default:
			return validationError(
				"unsupported_database_app_identity",
				"the discovered Spring application identity type is unsupported",
				"select the Container App system identity or one attached user-assigned identity",
			)
		}
	}
	return nil
}

// PreBuild validates packaging inputs without modifying authored build recipes.
func (r *Runtime) PreBuild(ctx context.Context, args *azdext.ServiceEventArgs) error {
	if args == nil || args.Project == nil || args.Service == nil {
		return validationError("missing_service_config", "service build configuration is missing", "check azure.yaml")
	}
	_, values, err := r.environments.values(ctx)
	if err != nil {
		return err
	}
	if _, err := r.resolveService(ctx, args.Project, args.Service, nil, values, false); err != nil {
		return err
	}
	if r.client != nil {
		if _, err := resolveProjectServiceModules(ctx, r.client, args.Project); err != nil {
			return err
		}
	}
	return prepareSpringBuild(args.Project, args.Service)
}

// PreDeploy verifies provisioned access before Spring initializes its application schema at startup.
func (r *Runtime) PreDeploy(ctx context.Context, args *azdext.ServiceEventArgs) error {
	return r.reconcileService(ctx, args)
}

// PostDeploy re-verifies managed database access after a successful deployment.
func (r *Runtime) PostDeploy(ctx context.Context, args *azdext.ServiceEventArgs) error {
	return r.reconcileService(ctx, args)
}

func (r *Runtime) reconcileService(ctx context.Context, args *azdext.ServiceEventArgs) (resultErr error) {
	if args == nil || args.Service == nil {
		return validationError("missing_service_config", "service deployment configuration is missing", "check azure.yaml")
	}
	envName, values, err := r.environments.values(ctx)
	if err != nil {
		return err
	}
	ctx, values, err = r.bindAutomaticProject(ctx, args.Project, envName, values)
	if err != nil {
		return err
	}
	closeNetwork, err := r.beginAutomaticNetwork(ctx)
	defer func() {
		if cleanupErr := closeNetwork(); cleanupErr != nil && !errors.Is(resultErr, cleanupErr) {
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}()
	if err != nil {
		return err
	}
	resolved, err := r.resolveService(ctx, args.Project, args.Service, args.ServiceContext, values, true)
	if err != nil {
		return err
	}
	if r.client != nil {
		if _, err := resolveProjectServiceModules(ctx, r.client, args.Project); err != nil {
			return err
		}
	}
	if resolved.database.managedAccess() {
		if values[databaseAccessConfiguredEnv] != databaseAccessPolicyVersion ||
			values[provisionedDatabaseAccessEnv] != resolved.database.accessFingerprint() {
			return validationError(
				"database_access_not_configured",
				"managed PostgreSQL application access is incomplete for this deployment",
				"run azd provision so the extension can verify the login, grants, identity, and cleanup",
			)
		}
		if r.outputs == nil {
			return errors.New("current deployment output verification is unavailable")
		}
		values["AZURE_ENV_NAME"] = envName
		binding, err := r.outputs.Verify(ctx, resolved.database, values)
		if err != nil {
			return err
		}
		if binding != nil {
			values = binding.values
			ctx = context.WithValue(ctx, deploymentBindingKey{}, binding)
		}
		if r.client != nil || (binding != nil && binding.app != nil) {
			if err := r.verifyExplicitDatasource(
				ctx, args.Project, args.Service, args.ServiceContext, resolved.database, values,
			); err != nil {
				return err
			}
		}
		resolved.database.AdministratorName = values[databaseAdministratorNameEnv]
		if resolved.database.discovered && resolved.database.Authentication == databaseAuthenticationPassword {
			values = maps.Clone(values)
			values[databaseAppPasswordEnv] = resolved.database.appPassword
		}
		if err := r.configureAccess(ctx, envName, resolved.database, values); err != nil {
			return err
		}
	}

	if resolved.mode == databasePostgreSQL {
		if err := r.reconcile(ctx, values); err != nil {
			return err
		}
	}
	return closeNetwork()
}
