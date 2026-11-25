// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"azurespringboot/internal/deploy"
	"azurespringboot/internal/packaging"
	"azurespringboot/internal/postgres"
	"azurespringboot/internal/springmodel"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Runtime implements the Spring host's lifecycle preparation.
type Runtime struct {
	client          *azdext.AzdClient
	environments    springmodel.EnvironmentStore
	identities      deploy.DeveloperIdentityResolver
	outputs         deploy.DeploymentOutputVerifier
	configureAccess func(context.Context, string, springmodel.ResolvedDatabase, map[string]string) error
	recoverAccess   func(context.Context, string, springmodel.ResolvedDatabase, map[string]string) error
	prepareNetwork  func(context.Context, *deploy.DeploymentBinding) (func(context.Context) error, error)
	noPrompt        bool
	prompt          func(context.Context, *azdext.PromptOptions) (string, error)
}

// NewRuntime creates a Spring lifecycle runtime backed by azd's stable services.
func NewRuntime(client *azdext.AzdClient, noPrompt bool) *Runtime {
	reconciler := postgres.NewLocalAccessReconciler(postgres.NewAccessReconciler(client))
	return &Runtime{
		client:          client,
		environments:    springmodel.NewGrpcEnvironmentStore(client),
		identities:      deploy.NewDeveloperIdentityResolver(client),
		outputs:         deploy.NewDeploymentOutputVerifier(client),
		configureAccess: reconciler.Reconcile,
		recoverAccess:   reconciler.Recover,
		prepareNetwork:  postgres.NewFirewallReconciler(client).PrepareAutomatic,
		noPrompt:        noPrompt,
		prompt: func(ctx context.Context, options *azdext.PromptOptions) (string, error) {
			response, err := client.Prompt().Prompt(ctx, &azdext.PromptRequest{Options: options})
			if err != nil {
				return "", err
			}
			return response.GetValue(), nil
		},
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
	if service.GetHost() != springmodel.SpringHost {
		return nil, validationError(
			"unsupported_spring_host",
			fmt.Sprintf("service %q uses unsupported Spring host %q", service.GetName(), service.GetHost()),
			fmt.Sprintf("set the service host to %s", springmodel.SpringHost),
		)
	}
	if err := packaging.ValidateServiceConfiguration(service); err != nil {
		return nil, err
	}
	if err := packaging.ValidateServiceConfiguration(service); err != nil {
		return nil, err
	}
	database, err := r.discoverServiceDatabase(ctx, project, service, serviceContext, values, requireLate)
	if err != nil {
		return nil, err
	}
	if provisioned := values[springmodel.ProvisionedDatabaseTypeEnv]; requireLate &&
		database.Type != springmodel.DatabaseNone && provisioned != "" &&
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
	if database.ManagedAccess() && !database.ServerDeferred {
		if provisioned := springmodel.ExpectedProvisionedAccess(values, service.GetName()); provisioned != "" &&
			provisioned != database.AccessFingerprint() {
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
) ([]*resolvedService, springmodel.ResolvedDatabase, error) {
	services := springmodel.SpringServices(project)
	if len(services) == 0 {
		return nil, springmodel.ResolvedDatabase{}, nil
	}

	resolved := make([]*resolvedService, 0, len(services))
	var dbServices []*resolvedService
	var selected springmodel.ResolvedDatabase
	hasDatabase := false
	for _, service := range services {
		serviceCtx, bindErr := deploy.BindServiceContext(ctx, service.GetName())
		if bindErr != nil {
			return nil, springmodel.ResolvedDatabase{}, bindErr
		}
		serviceValues := values
		if binding, present := serviceCtx.Value(deploy.DeploymentBindingKey{}).(*deploy.DeploymentBinding); present {
			serviceValues = binding.Values
		}
		item, err := r.resolveService(serviceCtx, project, service, nil, serviceValues, requireLate)
		if err != nil {
			return nil, springmodel.ResolvedDatabase{}, err
		}
		if item.mode != springmodel.DatabaseNone {
			for _, prior := range dbServices {
				if compatErr := checkCompatibleDatabaseSelection(prior.database, item.database); compatErr != nil {
					return nil, springmodel.ResolvedDatabase{}, compatErr
				}
			}
			if !hasDatabase {
				selected = item.database
				hasDatabase = true
			}
			dbServices = append(dbServices, item)
		}
		if validateFiles {
			if err := packaging.ValidateSpringBuild(project, service); err != nil {
				return nil, springmodel.ResolvedDatabase{}, err
			}
		}
		resolved = append(resolved, item)
	}
	if !hasDatabase {
		selected = springmodel.ResolvedDatabase{Type: springmodel.DatabaseNone, Discovered: true}
	}
	return resolved, selected, nil
}

func checkCompatibleDatabaseSelection(left, right springmodel.ResolvedDatabase) error {
	if left.Discovered && right.Discovered && (left.SelectionPending || right.SelectionPending) {
		return nil
	}
	if left.Type != right.Type || left.Authentication != right.Authentication {
		return validationError(
			"conflicting_service_database_configuration",
			"Spring services in one project resolve to incompatible database types or authentication modes",
			fmt.Sprintf(
				"use one compatible managed database access configuration for all %s services",
				springmodel.SpringHost,
			),
		)
	}
	if !left.ManagedAccess() || left.ServerDeferred || right.ServerDeferred {
		return nil
	}
	if !strings.EqualFold(left.ServerResourceID, right.ServerResourceID) {
		return validationError(
			"conflicting_service_database_configuration",
			"Spring services in one project resolve to different PostgreSQL servers",
			fmt.Sprintf("use one provisioned PostgreSQL Flexible Server for all %s services", springmodel.SpringHost),
		)
	}
	if left.Username != "" && left.Username == right.Username {
		if identityResourceID(left) != identityResourceID(right) {
			return validationError(
				"conflicting_service_database_role",
				"multiple Spring services with different managed identities cannot share one PostgreSQL role login",
				"configure a distinct SPRING_DATASOURCE_USERNAME for each service identity",
			)
		}
	}
	return nil
}

func identityResourceID(database springmodel.ResolvedDatabase) string {
	if database.AppIdentity == nil {
		return ""
	}
	return strings.ToLower(database.AppIdentity.ResourceID)
}

// ValidateService validates before azd writes or Azure operations.
func (r *Runtime) ValidateService(
	ctx context.Context, service *azdext.ServiceConfig, projects ...*azdext.ProjectConfig,
) error {
	_, values, err := r.environments.Values(ctx)
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
	if len(springmodel.SpringServices(args.Project)) == 0 {
		return nil
	}
	envName, values, err := r.environments.Values(ctx)
	if err != nil {
		return err
	}
	services, database, err := r.resolveProject(ctx, args.Project, values, true, false)
	if err != nil || len(services) == 0 {
		return err
	}
	if r.client != nil {
		if _, err := deploy.ResolveProjectServiceModules(ctx, r.client, args.Project); err != nil {
			return err
		}
	}
	preparation := database
	if database.Discovered {
		preparation, err = automaticProvisionInputs(ctx, args.Project, database)
		if err != nil {
			return err
		}
	}
	if preparation.Type == springmodel.DatabaseNone {
		return nil
	}
	writes := map[string]string{}
	switch preparation.Type {
	case springmodel.DatabaseH2:
		for _, name := range []string{
			springmodel.DatabasePasswordEnv,
			springmodel.DatabaseAppPasswordEnv,
			springmodel.DatabaseAuthenticationEnv,
			springmodel.DatabaseNameEnv,
			springmodel.DatabaseUsernameEnv,
			springmodel.DatabaseAdministratorNameEnv,
		} {
			if environmentValuePresent(values, os.LookupEnv, name) {
				return validationError(
					"postgres_configuration_not_supported_for_h2",
					fmt.Sprintf("%s cannot be supplied when the database type is h2", name),
					"remove PostgreSQL credentials and access bindings or select postgresql",
				)
			}
		}
	case springmodel.DatabasePostgreSQL:
		for _, name := range []string{springmodel.DatabasePasswordEnv, springmodel.DatabaseAppPasswordEnv} {
			if environmentValuePresent(values, os.LookupEnv, name) {
				return validationError(
					"password_not_supported_for_managed_identity",
					fmt.Sprintf("%s cannot be supplied for managedidentity auth", name),
					"remove administrator and application passwords from the selected environment and process",
				)
			}
		}
		administrator, resolveErr := resolveAdministratorInputs(preparation, values)
		if resolveErr != nil {
			return resolveErr
		}
		if r.noPrompt && administrator.Name == "" {
			return validationError(
				"missing_database_administrator_name",
				"a PostgreSQL Entra administrator name is required in no-prompt mode",
				"provide DATABASE_ADMIN_NAME; optionally provide DATABASE_ADMIN_OBJECT_ID to select another User",
			)
		}
		subscriptionID := strings.TrimSpace(values[springmodel.AzureSubscriptionIDEnv])
		if subscriptionID == "" {
			return validationError(
				"missing_azure_subscription",
				"AZURE_SUBSCRIPTION_ID is required to prepare the database administrator",
				"select the target azd subscription before provisioning",
			)
		}
		identity, resolveErr := r.identities.Resolve(ctx, subscriptionID)
		if resolveErr != nil {
			return resolveErr
		}
		if administrator.ObjectID == "" {
			administrator.ObjectID = identity.ObjectID
			if !r.noPrompt {
				administrator.ObjectID, resolveErr = r.promptAdministratorInput(ctx, &azdext.PromptOptions{
					Message:      "PostgreSQL Entra administrator User object ID:",
					HelpMessage:  "Use the signed-in User or enter another User's object ID in the subscription tenant.",
					DefaultValue: identity.ObjectID, Required: true,
				}, deploy.ValidAdministratorObjectID)
				if resolveErr != nil {
					return resolveErr
				}
			}
		}
		if administrator.Name == "" {
			administrator.Name, resolveErr = r.promptAdministratorInput(ctx, &azdext.PromptOptions{
				Message:     "PostgreSQL Entra administrator principal name:",
				HelpMessage: "Enter the selected User's PostgreSQL login name; azd does not verify directory ownership.",
				Required:    true,
			}, func(value string) bool {
				return deploy.ValidAdministratorName(value) && value != "azd-preprovision-pending"
			})
			if resolveErr != nil {
				return resolveErr
			}
		}
		for _, binding := range []struct {
			name          string
			value         string
			caseSensitive bool
		}{
			{springmodel.DatabaseAdministratorNameEnv, administrator.Name, true},
			{springmodel.DatabaseAdministratorObjectIDEnv, administrator.ObjectID, false},
			{springmodel.DatabaseUserAccessTenantIDEnv, identity.UserAccessTenantID, false},
			{"DATABASE_ADMIN_NAME", administrator.Name, true},
			{"DATABASE_ADMIN_OBJECT_ID", administrator.ObjectID, false},
		} {
			conflicts := func(existing string) bool {
				if binding.caseSensitive {
					return existing != binding.value
				}
				return !strings.EqualFold(existing, binding.value)
			}
			if existing, present := values[binding.name]; present &&
				binding.name != springmodel.DatabaseUserAccessTenantIDEnv && conflicts(existing) {
				return validationError(
					"conflicting_database_administrator_inputs",
					fmt.Sprintf("%s conflicts with the selected administrator", binding.name),
					"remove conflicting administrator inputs before provisioning",
				)
			}
			if existing, present := os.LookupEnv(binding.name); present && conflicts(existing) {
				return validationError(
					"conflicting_database_administrator_inputs",
					fmt.Sprintf("%s conflicts with the prepared administrator or access tenant", binding.name),
					"remove the conflicting process override before provisioning",
				)
			}
			writes[binding.name] = binding.value
		}
	}

	return persistEnvironmentWrites(ctx, r.environments, envName, values, writes)
}

func (r *Runtime) promptAdministratorInput(
	ctx context.Context, options *azdext.PromptOptions, valid func(string) bool,
) (string, error) {
	if r.prompt == nil {
		return "", errors.New("host administrator input is unavailable")
	}
	message := options.Message
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		value, err := r.prompt(ctx, options)
		if err != nil {
			return "", fmt.Errorf("collecting the PostgreSQL administrator input: %w", err)
		}
		if valid(value) {
			return value, nil
		}
		options.Message = "The value is invalid. " + message
	}
}

type administratorInputs struct {
	Name     string
	ObjectID string
}

func resolveAdministratorInputs(
	database springmodel.ResolvedDatabase, values map[string]string,
) (administratorInputs, error) {
	administrator := administratorInputs{Name: database.AdministratorName}
	for _, lookup := range []func(string) (string, bool){
		func(key string) (string, bool) { value, present := values[key]; return value, present },
		os.LookupEnv,
	} {
		if tenantID, present := lookup(springmodel.DatabaseUserAccessTenantIDEnv); present &&
			!deploy.ValidAdministratorObjectID(tenantID) {
			return administratorInputs{}, validationError(
				"invalid_database_administrator_input",
				fmt.Sprintf("%s is not a valid user-access tenant ID", springmodel.DatabaseUserAccessTenantIDEnv),
				"remove the override and let azd resolve the selected subscription's user-access tenant",
			)
		}
	}
	for _, field := range []struct {
		name string
		isID bool
	}{
		{springmodel.DatabaseAdministratorNameEnv, false},
		{"DATABASE_ADMIN_NAME", false},
		{springmodel.DatabaseAdministratorObjectIDEnv, true},
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
			valid := deploy.ValidAdministratorName(candidate) && candidate != "azd-preprovision-pending"
			selected := &administrator.Name
			if field.isID {
				valid = deploy.ValidAdministratorObjectID(candidate)
				selected = &administrator.ObjectID
			}
			if !valid {
				return administratorInputs{}, validationError(
					"invalid_database_administrator_input",
					fmt.Sprintf("%s is not a valid administrator identity", field.name),
					"provide a nonempty User administrator name and, when selecting another User, a nonzero object ID",
				)
			}
			matches := *selected == candidate
			if field.isID {
				matches = strings.EqualFold(*selected, candidate)
			}
			if *selected != "" && !matches {
				return administratorInputs{}, validationError(
					"conflicting_database_administrator_inputs",
					fmt.Sprintf("%s conflicts with another explicit administrator input", field.name),
					"make DATABASE_ADMIN_* and SPRING_DATABASE_ADMINISTRATOR_* identify the same User administrator",
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
	environments springmodel.EnvironmentStore,
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
		if err := environments.Set(ctx, envName, key, writes[key]); err != nil {
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
	if len(springmodel.SpringServices(args.Project)) == 0 {
		return nil
	}
	envName, values, err := r.environments.Values(ctx)
	if err != nil {
		return err
	}
	ctx, values, err = r.bindAutomaticProject(ctx, args.Project, envName, values)
	if err != nil {
		return err
	}
	services, database, err := r.resolveProject(ctx, args.Project, values, false, true)
	if err != nil || len(services) == 0 {
		return err
	}
	if r.client != nil {
		if _, err := deploy.ResolveProjectServiceModules(ctx, r.client, args.Project); err != nil {
			return err
		}
	}
	if database.Type == springmodel.DatabaseNone {
		return nil
	}
	if database.Type != springmodel.DatabasePostgreSQL {
		return persistEnvironmentWrites(ctx, r.environments, envName, values, map[string]string{
			springmodel.ProvisionedDatabaseTypeEnv: string(database.Type),
		})
	}
	var pgServices []*resolvedService
	for _, service := range services {
		if service.mode == springmodel.DatabasePostgreSQL {
			pgServices = append(pgServices, service)
		}
	}
	baseCtx := ctx
	ctx, err = deploy.BindServiceContext(baseCtx, pgServices[0].service.GetName())
	if err != nil {
		return err
	}
	if binding, present := ctx.Value(deploy.DeploymentBindingKey{}).(*deploy.DeploymentBinding); present {
		values = binding.Values
	}
	if database.Discovered && database.Authentication == springmodel.DatabaseAuthenticationManagedIdentity {
		database.AdministratorName = strings.TrimSpace(values[springmodel.DatabaseAdministratorNameEnv])
		if !deploy.ValidAdministratorName(database.AdministratorName) ||
			!deploy.ValidAdministratorObjectID(strings.TrimSpace(values[springmodel.DatabaseAdministratorObjectIDEnv])) {
			return configurationError("the configured PostgreSQL User administrator identity is incomplete")
		}
		if err := r.environments.Set(ctx, envName, springmodel.DatabaseAccessConfiguredEnv, "pending"); err != nil {
			return err
		}
		values = maps.Clone(values)
		values[springmodel.DatabaseAccessConfiguredEnv] = "pending"
		if binding, present := baseCtx.Value(deploy.DeploymentBindingKey{}).(*deploy.DeploymentBinding); present {
			pendingBase := *binding
			pendingBase.Values = maps.Clone(binding.Values)
			pendingBase.Values[springmodel.DatabaseAccessConfiguredEnv] = "pending"
			baseCtx = context.WithValue(baseCtx, deploy.DeploymentBindingKey{}, &pendingBase)
		}
		if binding, present := ctx.Value(deploy.DeploymentBindingKey{}).(*deploy.DeploymentBinding); present {
			pending := *binding
			pending.Values = values
			ctx = context.WithValue(ctx, deploy.DeploymentBindingKey{}, &pending)
		}
		if r.recoverAccess != nil {
			if err := r.recoverAccess(ctx, envName, database, values); err != nil {
				return err
			}
		}
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
	if r.outputs == nil {
		return errors.New("current deployment output verification is unavailable")
	}
	type preparedPostgresTarget struct {
		service  *azdext.ServiceConfig
		database springmodel.ResolvedDatabase
		ctx      context.Context
		values   map[string]string
		binding  *deploy.DeploymentBinding
	}
	prepared := make([]preparedPostgresTarget, 0, len(pgServices))
	for _, pgSvc := range pgServices {
		svcCtx, bindErr := deploy.BindServiceContext(baseCtx, pgSvc.service.GetName())
		if bindErr != nil {
			return bindErr
		}
		svcValues := maps.Clone(values)
		if bound, present := svcCtx.Value(deploy.DeploymentBindingKey{}).(*deploy.DeploymentBinding); present {
			svcValues = maps.Clone(bound.Values)
			if values[springmodel.DatabaseAccessConfiguredEnv] != "" {
				svcValues[springmodel.DatabaseAccessConfiguredEnv] = values[springmodel.DatabaseAccessConfiguredEnv]
			}
		}
		svcValues["AZURE_ENV_NAME"] = envName
		verifiedBinding, verifyErr := r.outputs.Verify(svcCtx, pgSvc.database, svcValues)
		if verifyErr != nil {
			return verifyErr
		}
		if verifiedBinding != nil {
			svcValues = verifiedBinding.Values
			svcCtx = context.WithValue(svcCtx, deploy.DeploymentBindingKey{}, verifiedBinding)
		}
		if err := deploy.ValidateProvisionedBindings(pgSvc.database, svcValues); err != nil {
			return err
		}
		prepared = append(prepared, preparedPostgresTarget{
			service: pgSvc.service, database: pgSvc.database,
			ctx: svcCtx, values: svcValues, binding: verifiedBinding,
		})
	}
	values = prepared[0].values
	ctx = prepared[0].ctx
	database.AdministratorName = strings.TrimSpace(values[springmodel.DatabaseAdministratorNameEnv])
	if !deploy.ValidAdministratorName(database.AdministratorName) {
		return validationError(
			"missing_database_administrator_name",
			"the configured PostgreSQL User administrator login is missing or invalid",
			"set DATABASE_ADMIN_NAME and run azd provision again",
		)
	}
	if strings.TrimSpace(values[springmodel.DatabaseAdministratorObjectIDEnv]) == "" ||
		strings.TrimSpace(values[springmodel.DatabaseUserAccessTenantIDEnv]) == "" {
		return validationError(
			"missing_database_administrator_identity",
			"the configured PostgreSQL User administrator identity is incomplete",
			"run azd provision again with the selected administrator inputs and subscription",
		)
	}
	reconciledTargets := map[string]bool{}
	for _, target := range prepared {
		targetDB := target.database
		targetDB.AdministratorName = database.AdministratorName
		fp := targetDB.AccessFingerprint()
		if reconciledTargets[fp] {
			continue
		}
		if err := r.configureAccess(target.ctx, envName, targetDB, target.values); err != nil {
			return err
		}
		reconciledTargets[fp] = true
	}
	if err := closeNetwork(); err != nil {
		return err
	}
	writes := map[string]string{
		springmodel.ProvisionedDatabaseTypeEnv:  string(database.Type),
		springmodel.DatabaseAccessConfiguredEnv: databaseProvisioningVersion(database),
	}
	if len(pgServices) == 1 {
		writes[springmodel.ProvisionedDatabaseAccessEnv] = database.AccessFingerprint()
		if database.Discovered {
			writes[springmodel.DatabaseTypeEnv] = string(database.Type)
			writes[springmodel.DatabaseAuthenticationEnv] = string(database.Authentication)
			writes[springmodel.DatabaseNameEnv] = database.Name
			writes[springmodel.DatabaseUsernameEnv] = database.Username
		}
	} else {
		fingerprints := make([]string, 0, len(pgServices))
		for _, pgSvc := range pgServices {
			fp := pgSvc.database.AccessFingerprint()
			writes[springmodel.ServiceProvisionedAccessEnvKey(pgSvc.service.GetName())] = fp
			fingerprints = append(fingerprints, pgSvc.service.GetName()+"="+fp)
		}
		writes[springmodel.ProvisionedDatabaseAccessEnv] = "multi:" + strings.Join(fingerprints, ";")
		if database.Discovered {
			writes[springmodel.DatabaseTypeEnv] = string(database.Type)
			writes[springmodel.DatabaseAuthenticationEnv] = string(database.Authentication)
		}
	}
	if database.AdministratorName != "" {
		writes[springmodel.DatabaseAdministratorNameEnv] = database.AdministratorName
	}
	return persistEnvironmentWrites(ctx, r.environments, envName, values, writes)
}

// PreBuild validates packaging inputs without modifying authored build recipes.
func (r *Runtime) PreBuild(ctx context.Context, args *azdext.ServiceEventArgs) error {
	if args == nil || args.Project == nil || args.Service == nil {
		return validationError("missing_service_config", "service build configuration is missing", "check azure.yaml")
	}
	_, values, err := r.environments.Values(ctx)
	if err != nil {
		return err
	}
	if _, err := r.resolveService(ctx, args.Project, args.Service, nil, values, false); err != nil {
		return err
	}
	if r.client != nil {
		if _, err := deploy.ResolveProjectServiceModules(ctx, r.client, args.Project); err != nil {
			return err
		}
	}
	return packaging.PrepareSpringBuild(args.Project, args.Service)
}

// PreDeploy verifies provisioned access before Spring initializes its application schema at startup.
func (r *Runtime) PreDeploy(ctx context.Context, args *azdext.ServiceEventArgs) error {
	return r.reconcileService(ctx, args)
}

func (r *Runtime) reconcileService(ctx context.Context, args *azdext.ServiceEventArgs) (resultErr error) {
	if args == nil || args.Service == nil {
		return validationError("missing_service_config", "service deployment configuration is missing", "check azure.yaml")
	}
	envName, values, err := r.environments.Values(ctx)
	if err != nil {
		return err
	}
	ctx, values, err = r.bindAutomaticProject(ctx, args.Project, envName, values)
	if err != nil {
		return err
	}
	ctx, err = deploy.BindServiceContext(ctx, args.Service.GetName())
	if err != nil {
		return err
	}
	if binding, present := ctx.Value(deploy.DeploymentBindingKey{}).(*deploy.DeploymentBinding); present {
		values = binding.Values
	}
	resolved, err := r.resolveService(ctx, args.Project, args.Service, args.ServiceContext, values, true)
	if err != nil {
		return err
	}
	if r.client != nil {
		if _, err := deploy.ResolveProjectServiceModules(ctx, r.client, args.Project); err != nil {
			return err
		}
	}
	if resolved.mode != springmodel.DatabasePostgreSQL {
		return nil
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
	if resolved.database.ManagedAccess() {
		if values[springmodel.DatabaseAccessConfiguredEnv] != databaseProvisioningVersion(resolved.database) ||
			springmodel.ExpectedProvisionedAccess(values, args.Service.GetName()) != resolved.database.AccessFingerprint() {
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
		if _, err := r.outputs.Verify(ctx, resolved.database, values); err != nil {
			return err
		}
	}

	return closeNetwork()
}

func databaseProvisioningVersion(database springmodel.ResolvedDatabase) string {
	if database.Discovered && database.Authentication == springmodel.DatabaseAuthenticationManagedIdentity {
		return springmodel.AutomaticMIProvisioningVersion
	}
	return springmodel.DatabaseAccessPolicyVersion
}
