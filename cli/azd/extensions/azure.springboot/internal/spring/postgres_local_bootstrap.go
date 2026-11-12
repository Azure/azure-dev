// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/gofrs/flock"
	"github.com/jackc/pgx/v5/pgconn"
)

type postgresLocalCloud interface {
	administrator(context.Context, string) (*postgresBootstrapAdministrator, error)
	createAdministrator(context.Context, string, postgresBootstrapAdministrator) error
	removeAdministrator(context.Context, string, postgresBootstrapAdministrator) error
	firewallAvailable(context.Context, string) error
	createFirewall(context.Context, string) error
	removeFirewall(context.Context, string) error
}

type postgresLocalAccessReconciler struct {
	verifyResources func(context.Context, string, resolvedDatabase, map[string]string) (
		*verifiedPostgresResources, azcore.TokenCredential, error)
	resolveCaller func(context.Context, string) (*developerIdentity, error)
	newCloud      func(*verifiedPostgresResources, azcore.TokenCredential) (postgresLocalCloud, error)
	sql           func(context.Context, *verifiedPostgresResources, azcore.TokenCredential,
		resolvedDatabase, map[string]string, string) error
	stateDir      func() (string, error)
	suffix        func() (string, error)
	wait          func(context.Context, time.Duration) error
	recoverLegacy func(context.Context, *deploymentBinding, string, resolvedDatabase,
		*verifiedPostgresResources, azcore.TokenCredential) error
}

type postgresLocalBootstrapState struct {
	ServerID        string                          `json:"serverId"`
	Environment     string                          `json:"environment"`
	Suffix          string                          `json:"suffix"`
	AdministratorID string                          `json:"administratorId,omitempty"`
	Administrator   *postgresBootstrapAdministrator `json:"administrator,omitempty"`
}

func newPostgresLocalAccessReconciler(local *postgresAccessReconciler) *postgresLocalAccessReconciler {
	return &postgresLocalAccessReconciler{
		verifyResources: local.verifyResources,
		resolveCaller:   newDeveloperIdentityResolver(local.client).Resolve,
		newCloud:        newAzurePostgresLocalCloud,
		sql:             local.reconcileSQL,
		suffix:          postgresBootstrapRandomSuffix,
		wait:            sleepWithContext,
		recoverLegacy:   recoverLegacyPostgresBootstrap,
		stateDir: func() (string, error) {
			projectDir, err := azdext.GetProjectDir()
			if err != nil {
				return "", err
			}
			return filepath.Join(projectDir, ".azure", "spring-postgres-bootstrap"), nil
		},
	}
}

func (s postgresLocalBootstrapState) firewallName() string {
	return bootstrapFirewallRulePrefix(s.Environment, s.ServerID) + "local-" + s.Suffix
}

func (s postgresLocalBootstrapState) validate(serverID, envName, selectedAdministratorID string) error {
	suffix, err := hex.DecodeString(s.Suffix)
	if err != nil || len(suffix) != 8 || !strings.EqualFold(s.ServerID, serverID) || s.Environment != envName {
		return configurationError("the local PostgreSQL bootstrap recovery record has invalid ownership")
	}
	if s.Administrator == nil {
		if s.AdministratorID != "" {
			return configurationError("the local PostgreSQL bootstrap administrator record is incomplete")
		}
		return nil
	}
	prefix := serverID + "/administrators/"
	objectID := strings.TrimPrefix(s.AdministratorID, prefix)
	if !strings.HasPrefix(s.AdministratorID, prefix) || !validAdministratorObjectID(objectID) ||
		strings.EqualFold(s.AdministratorID, selectedAdministratorID) ||
		s.Administrator.PrincipalName != "azd-local-"+s.Suffix ||
		s.Administrator.PrincipalType != "User" || !validAdministratorObjectID(s.Administrator.TenantID) {
		return configurationError("the local PostgreSQL bootstrap administrator ownership cannot be verified")
	}
	return nil
}

func (r *postgresLocalAccessReconciler) Reconcile(
	ctx context.Context, envName string, database resolvedDatabase, values map[string]string,
) (resultErr error) {
	resources, credential, err := r.verifyResources(ctx, envName, database, values)
	if err != nil {
		return err
	}
	if !resources.PublicNetworkAccessEnabled {
		return configurationError("local PostgreSQL bootstrap requires an explicitly enabled public endpoint")
	}
	caller, err := r.resolveCaller(ctx, resources.SubscriptionID)
	if err != nil {
		return err
	}
	cloud, err := r.newCloud(resources, credential)
	if err != nil {
		return err
	}
	path, unlock, err := r.lockState(resources.ServerID)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, unlock()) }()

	selectedID := resources.ServerID + "/administrators/" + values[databaseAdministratorObjectIDEnv]
	if err := recoverLocalPostgresBootstrap(ctx, cloud, path, resources.ServerID, envName, selectedID); err != nil {
		return err
	}
	selected, err := cloud.administrator(ctx, selectedID)
	if err != nil {
		return err
	}
	if selected.PrincipalName != resources.AdministratorName || selected.PrincipalType != "User" ||
		!validAdministratorObjectID(selected.TenantID) {
		return configurationError("the persistent selected PostgreSQL administrator changed before bootstrap")
	}
	suffix, err := r.suffix()
	if err != nil {
		return err
	}
	state := postgresLocalBootstrapState{ServerID: resources.ServerID, Environment: envName, Suffix: suffix}
	callerID := resources.ServerID + "/administrators/" + caller.ObjectID
	administrator, err := cloud.administrator(ctx, callerID)
	if isAzureNotFound(err) {
		state.AdministratorID = callerID
		state.Administrator = &postgresBootstrapAdministrator{
			PrincipalName: "azd-local-" + suffix, PrincipalType: "User", TenantID: selected.TenantID,
		}
		administrator = state.Administrator
	} else if err != nil {
		return err
	} else if strings.HasPrefix(administrator.PrincipalName, "azd-local-") {
		return configurationError("another caller bootstrap administrator requires recovery by its owning project")
	}
	if administrator.PrincipalType != "User" || !strings.EqualFold(administrator.TenantID, selected.TenantID) ||
		!validAdministratorName(administrator.PrincipalName) {
		return configurationError("the caller PostgreSQL administrator has an unexpected type or tenant")
	}
	if err := state.validate(resources.ServerID, envName, selectedID); err != nil {
		return err
	}
	if err := cloud.firewallAvailable(ctx, state.firewallName()); err != nil {
		return err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	// Flush ownership intent before either Azure write, including ambiguous partial successes.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("recording PostgreSQL bootstrap ownership: %w", err)
	}
	_, writeErr := file.Write(encoded)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return fmt.Errorf("saving PostgreSQL bootstrap ownership: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr,
			recoverLocalPostgresBootstrap(ctx, cloud, path, resources.ServerID, envName, selectedID))
	}()
	if state.Administrator != nil {
		if err := cloud.createAdministrator(ctx, callerID, *state.Administrator); err != nil {
			return err
		}
	}
	if err := cloud.createFirewall(ctx, state.firewallName()); err != nil {
		return err
	}
	session := *resources
	session.SessionAdministratorName = administrator.PrincipalName
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		err := r.sql(readyCtx, &session, credential, database, values, envName)
		if err == nil {
			return nil
		}
		if !isPostgresLocalReadinessError(err) {
			return err
		}
		if waitErr := r.wait(readyCtx, 5*time.Second); waitErr != nil {
			return fmt.Errorf("waiting for local PostgreSQL bootstrap access: %w", errors.Join(err, waitErr))
		}
	}
}

func (r *postgresLocalAccessReconciler) lockState(serverID string) (string, func() error, error) {
	directory, err := r.stateDir()
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", nil, fmt.Errorf("creating PostgreSQL bootstrap recovery directory: %w", err)
	}
	path := filepath.Join(directory, postgresBootstrapServerTagValue(serverID)+".json")
	lock := flock.New(path + ".lock")
	locked, err := lock.TryLock()
	if err != nil {
		return "", nil, fmt.Errorf("locking PostgreSQL bootstrap recovery state: %w", err)
	}
	if !locked {
		return "", nil, configurationError("another local PostgreSQL bootstrap is in progress; retry after it finishes")
	}
	return path, lock.Unlock, nil
}

func (r *postgresLocalAccessReconciler) Recover(
	ctx context.Context, envName string, database resolvedDatabase, values map[string]string,
) (resultErr error) {
	resources, credential, err := r.verifyResources(ctx, envName, database, values)
	if err != nil {
		return err
	}
	cloud, err := r.newCloud(resources, credential)
	if err != nil {
		return err
	}
	path, unlock, err := r.lockState(resources.ServerID)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, unlock()) }()
	if err := recoverLocalPostgresBootstrap(ctx, cloud, path, resources.ServerID, envName,
		resources.ServerID+"/administrators/"+values[databaseAdministratorObjectIDEnv]); err != nil {
		return err
	}
	if binding, present := ctx.Value(deploymentBindingKey{}).(*deploymentBinding); present && r.recoverLegacy != nil {
		return r.recoverLegacy(ctx, binding, envName, database, resources, credential)
	}
	return nil
}

func isPostgresLocalReadinessError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	connection, ok := errors.AsType[*postgresConnectionError](err)
	if !ok {
		return false
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](connection.cause); ok {
		return pgErr.Code == "28000" || pgErr.Code == "28P01" || isTransientPostgresError(connection.cause)
	}
	return isTransientPostgresError(connection.cause) ||
		errors.Is(connection.cause, io.EOF) || errors.Is(connection.cause, io.ErrUnexpectedEOF) ||
		strings.Contains(strings.ToLower(connection.cause.Error()), "forcibly closed")
}

func recoverLocalPostgresBootstrap(
	ctx context.Context, cloud postgresLocalCloud, path, serverID, envName, selectedID string,
) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading PostgreSQL bootstrap recovery state: %w", err)
	}
	var state postgresLocalBootstrapState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decoding PostgreSQL bootstrap recovery state: %w", err)
	}
	if err := state.validate(serverID, envName, selectedID); err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postgresCleanupTimeout)
	firewallErr := cloud.removeFirewall(cleanupCtx, state.firewallName())
	cancel()
	var administratorErr error
	if state.Administrator != nil {
		cleanupCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), postgresCleanupTimeout)
		administratorErr = cloud.removeAdministrator(cleanupCtx, state.AdministratorID, *state.Administrator)
		cancel()
	}
	if err := errors.Join(firewallErr, administratorErr); err != nil {
		return fmt.Errorf("closing temporary PostgreSQL bootstrap access; recovery record retained: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing completed PostgreSQL bootstrap recovery record: %w", err)
	}
	return nil
}

type azurePostgresLocalCloud struct {
	resources *armresources.Client
	firewalls *armpostgresqlflexibleservers.FirewallRulesClient
	server    *verifiedPostgresResources
}

func newAzurePostgresLocalCloud(
	server *verifiedPostgresResources, credential azcore.TokenCredential,
) (postgresLocalCloud, error) {
	resources, err := armresources.NewClient(server.SubscriptionID, credential, nil)
	if err != nil {
		return nil, err
	}
	firewalls, err := armpostgresqlflexibleservers.NewFirewallRulesClient(server.SubscriptionID, credential, nil)
	if err != nil {
		return nil, err
	}
	return &azurePostgresLocalCloud{resources: resources, firewalls: firewalls, server: server}, nil
}

func (c *azurePostgresLocalCloud) administrator(
	ctx context.Context, id string,
) (*postgresBootstrapAdministrator, error) {
	response, err := c.resources.GetByID(ctx, id, postgresResourceAPIVersion, nil)
	if err != nil {
		return nil, err
	}
	properties, ok := response.Properties.(map[string]any)
	if !ok || response.ID == nil || !strings.EqualFold(*response.ID, id) ||
		!strings.EqualFold(fmt.Sprint(properties["objectId"]), id[strings.LastIndex(id, "/")+1:]) {
		return nil, configurationError("the PostgreSQL caller administrator identity could not be verified")
	}
	data, err := json.Marshal(properties)
	if err != nil {
		return nil, err
	}
	var administrator postgresBootstrapAdministrator
	if err := json.Unmarshal(data, &administrator); err != nil {
		return nil, err
	}
	return &administrator, nil
}

func (c *azurePostgresLocalCloud) createAdministrator(
	ctx context.Context, id string, administrator postgresBootstrapAdministrator,
) error {
	if _, err := c.administrator(ctx, id); !isAzureNotFound(err) {
		return errors.Join(
			configurationError("the temporary caller administrator already exists; creation was refused"), err)
	}
	poller, err := c.resources.BeginCreateOrUpdateByID(ctx, id, postgresResourceAPIVersion,
		armresources.GenericResource{Properties: administrator}, nil)
	if err != nil {
		return err
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	actual, err := c.administrator(ctx, id)
	if err != nil {
		return err
	}
	if !samePostgresAdministrator(*actual, administrator) {
		return configurationError("the temporary PostgreSQL caller administrator read-back changed")
	}
	return nil
}

func (c *azurePostgresLocalCloud) removeAdministrator(
	ctx context.Context, id string, expected postgresBootstrapAdministrator,
) error {
	actual, err := c.administrator(ctx, id)
	if isAzureNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !samePostgresAdministrator(*actual, expected) {
		return configurationError("the temporary PostgreSQL caller administrator changed; deletion was refused")
	}
	poller, err := c.resources.BeginDeleteByID(ctx, id, postgresResourceAPIVersion, nil)
	if err != nil && !isAzureNotFound(err) {
		return err
	}
	if err == nil {
		if _, err := poller.PollUntilDone(ctx, nil); err != nil && !isAzureNotFound(err) {
			return err
		}
	}
	_, err = c.administrator(ctx, id)
	if !isAzureNotFound(err) {
		return errors.Join(configurationError("temporary PostgreSQL administrator absence could not be verified"), err)
	}
	return nil
}

func (c *azurePostgresLocalCloud) createFirewall(ctx context.Context, name string) error {
	poller, err := c.firewalls.BeginCreateOrUpdate(ctx, c.server.ResourceGroup, c.server.ServerName, name,
		armpostgresqlflexibleservers.FirewallRule{
			Properties: &armpostgresqlflexibleservers.FirewallRuleProperties{
				StartIPAddress: new("0.0.0.0"), EndIPAddress: new("255.255.255.255"),
			},
		}, nil)
	if err != nil {
		return err
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	rule, err := c.firewalls.Get(ctx, c.server.ResourceGroup, c.server.ServerName, name, nil)
	if err != nil {
		return err
	}
	if rule.ID == nil || !strings.EqualFold(*rule.ID, c.server.ServerID+"/firewallRules/"+name) ||
		!isAllPublicIPv4Rule(rule.Properties) {
		return configurationError("the temporary PostgreSQL firewall rule read-back changed")
	}
	return nil
}

func (c *azurePostgresLocalCloud) firewallAvailable(ctx context.Context, name string) error {
	_, err := c.firewalls.Get(ctx, c.server.ResourceGroup, c.server.ServerName, name, nil)
	if !isAzureNotFound(err) {
		return errors.Join(configurationError("the temporary PostgreSQL firewall name is not available"), err)
	}
	return nil
}

func samePostgresAdministrator(actual, expected postgresBootstrapAdministrator) bool {
	return actual.PrincipalName == expected.PrincipalName && actual.PrincipalType == expected.PrincipalType &&
		strings.EqualFold(actual.TenantID, expected.TenantID)
}

func (c *azurePostgresLocalCloud) removeFirewall(ctx context.Context, name string) error {
	rule, err := c.firewalls.Get(ctx, c.server.ResourceGroup, c.server.ServerName, name, nil)
	if isAzureNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if rule.ID == nil || !strings.EqualFold(*rule.ID, c.server.ServerID+"/firewallRules/"+name) ||
		!isAllPublicIPv4Rule(rule.Properties) {
		return configurationError("the owned PostgreSQL bootstrap firewall rule changed; deletion was refused")
	}
	if err := deleteBootstrapFirewallRule(ctx, c.firewalls, c.server.ResourceGroup, c.server.ServerName, name); err != nil {
		return err
	}
	_, err = c.firewalls.Get(ctx, c.server.ResourceGroup, c.server.ServerName, name, nil)
	if !isAzureNotFound(err) {
		return errors.Join(configurationError("temporary PostgreSQL firewall absence could not be verified"), err)
	}
	return nil
}
