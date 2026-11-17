// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

const (
	localTestSelectedID = "11111111-1111-1111-1111-111111111111"
	localTestCallerID   = "22222222-2222-2222-2222-222222222222"
	localTestTenantID   = "33333333-3333-3333-3333-333333333333"
)

type localBootstrapTestCloud struct {
	administrators map[string]postgresBootstrapAdministrator
	firewalls      map[string]bool
	operations     []string
	createAdminErr error
	createRuleErr  error
	removeRuleErr  error
	removeAdminErr error
}

func (c *localBootstrapTestCloud) administrator(
	_ context.Context, id string,
) (*postgresBootstrapAdministrator, error) {
	admin, present := c.administrators[id]
	if !present {
		return nil, &azcore.ResponseError{StatusCode: 404}
	}
	return &admin, nil
}

func (c *localBootstrapTestCloud) createAdministrator(
	_ context.Context, id string, administrator postgresBootstrapAdministrator,
) error {
	c.operations = append(c.operations, "create-administrator")
	c.administrators[id] = administrator
	return c.createAdminErr
}

func (c *localBootstrapTestCloud) removeAdministrator(
	ctx context.Context, id string, administrator postgresBootstrapAdministrator,
) error {
	c.operations = append(c.operations, "remove-administrator")
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.removeAdminErr != nil {
		return c.removeAdminErr
	}
	if actual, present := c.administrators[id]; present && actual != administrator {
		return errors.New("administrator ownership changed")
	}
	delete(c.administrators, id)
	return nil
}

func (c *localBootstrapTestCloud) createFirewall(_ context.Context, name string) error {
	c.operations = append(c.operations, "create-firewall")
	c.firewalls[name] = true
	return c.createRuleErr
}

func (c *localBootstrapTestCloud) firewallAvailable(_ context.Context, name string) error {
	if c.firewalls[name] {
		return errors.New("firewall name unavailable")
	}
	return nil
}

func (c *localBootstrapTestCloud) removeFirewall(ctx context.Context, name string) error {
	c.operations = append(c.operations, "remove-firewall")
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.removeRuleErr != nil {
		return c.removeRuleErr
	}
	delete(c.firewalls, name)
	return nil
}

func newLocalBootstrapFixture(t *testing.T, callerID string) (
	*postgresLocalAccessReconciler, *localBootstrapTestCloud, map[string]string, string,
) {
	t.Helper()
	server := &verifiedPostgresResources{
		SubscriptionID: "sub", ResourceGroup: "rg", ServerName: "server",
		ServerID: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.DBforPostgreSQL/flexibleServers/server",
		AdministratorName: "selected-user", PublicNetworkAccessEnabled: true,
	}
	cloud := &localBootstrapTestCloud{
		administrators: map[string]postgresBootstrapAdministrator{
			server.ServerID + "/administrators/" + localTestSelectedID: {
				PrincipalName: server.AdministratorName, PrincipalType: "User", TenantID: localTestTenantID,
			},
		},
		firewalls: map[string]bool{},
	}
	directory := t.TempDir()
	reconciler := &postgresLocalAccessReconciler{
		verifyResources: func(context.Context, string, resolvedDatabase, map[string]string) (
			*verifiedPostgresResources, azcore.TokenCredential, error,
		) {
			return server, nil, nil
		},
		resolveCaller: func(context.Context, string) (*developerIdentity, error) {
			return &developerIdentity{ObjectID: callerID, UserAccessTenantID: localTestTenantID}, nil
		},
		newCloud: func(*verifiedPostgresResources, azcore.TokenCredential) (postgresLocalCloud, error) {
			return cloud, nil
		},
		sql: func(_ context.Context, session *verifiedPostgresResources, _ azcore.TokenCredential,
			_ resolvedDatabase, _ map[string]string, _ string,
		) error {
			cloud.operations = append(cloud.operations, "sql")
			require.Equal(t, "selected-user", session.AdministratorName)
			if callerID == localTestSelectedID {
				require.Equal(t, "selected-user", session.sessionAdministratorName())
			}
			require.Len(t, cloud.firewalls, 1)
			return nil
		},
		stateDir: func() (string, error) { return directory, nil },
		suffix:   func() (string, error) { return "0123456789abcdef", nil },
		wait:     func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
	}
	values := map[string]string{databaseAdministratorObjectIDEnv: localTestSelectedID}
	path := filepath.Join(directory, postgresBootstrapServerTagValue(server.ServerID)+".json")
	return reconciler, cloud, values, path
}

func TestLocalPostgresBootstrapPreservesSelectedAndExistingCallerAdministrators(t *testing.T) {
	for _, test := range []struct {
		name        string
		callerID    string
		existing    bool
		expectedOps []string
	}{
		{name: "selected user", callerID: localTestSelectedID,
			expectedOps: []string{"create-firewall", "sql", "remove-firewall"}},
		{name: "different user", callerID: localTestCallerID,
			expectedOps: []string{"create-administrator", "create-firewall", "sql",
				"remove-firewall", "remove-administrator"}},
		{name: "existing caller alias", callerID: localTestCallerID, existing: true,
			expectedOps: []string{"create-firewall", "sql", "remove-firewall"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, cloud, values, path := newLocalBootstrapFixture(t, test.callerID)
			server, _, err := r.verifyResources(t.Context(), "", resolvedDatabase{}, values)
			require.NoError(t, err)
			if test.existing {
				cloud.administrators[server.ServerID+"/administrators/"+test.callerID] =
					postgresBootstrapAdministrator{
						PrincipalName: "existing-alias", PrincipalType: "User", TenantID: localTestTenantID,
					}
				r.sql = func(_ context.Context, session *verifiedPostgresResources, _ azcore.TokenCredential,
					_ resolvedDatabase, _ map[string]string, _ string,
				) error {
					require.Equal(t, "existing-alias", session.sessionAdministratorName())
					cloud.operations = append(cloud.operations, "sql")
					return nil
				}
			}
			require.NoError(t, r.Reconcile(t.Context(), "dev", resolvedDatabase{}, values))
			require.Equal(t, test.expectedOps, cloud.operations)
			require.NoFileExists(t, path)
			require.Empty(t, cloud.firewalls)
			require.Equal(t, "selected-user",
				cloud.administrators[server.ServerID+"/administrators/"+localTestSelectedID].PrincipalName)
			expectedCount := 1
			if test.existing {
				expectedCount++
			}
			require.Len(t, cloud.administrators, expectedCount)
		})
	}
}

func TestLocalPostgresBootstrapCleansPartialSuccessAndCancellation(t *testing.T) {
	for _, failure := range []string{"administrator", "firewall", "sql", "cancellation"} {
		t.Run(failure, func(t *testing.T) {
			r, cloud, values, path := newLocalBootstrapFixture(t, localTestCallerID)
			expected := errors.New("operation failed")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch failure {
			case "administrator":
				cloud.createAdminErr = expected
			case "firewall":
				cloud.createRuleErr = expected
			default:
				r.sql = func(context.Context, *verifiedPostgresResources, azcore.TokenCredential,
					resolvedDatabase, map[string]string, string,
				) error {
					if failure == "cancellation" {
						cancel()
						return context.Canceled
					}
					return expected
				}
			}
			err := r.Reconcile(ctx, "dev", resolvedDatabase{}, values)
			if failure == "cancellation" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, expected)
			}
			require.NoFileExists(t, path)
			require.Empty(t, cloud.firewalls)
			require.Len(t, cloud.administrators, 1)
			require.Equal(t, "remove-firewall", cloud.operations[len(cloud.operations)-2])
			require.Equal(t, "remove-administrator", cloud.operations[len(cloud.operations)-1])
		})
	}
}

func TestLocalPostgresBootstrapRecoversOwnedResidueBeforeNetworkValidation(t *testing.T) {
	r, cloud, values, path := newLocalBootstrapFixture(t, localTestCallerID)
	cloud.removeRuleErr = errors.New("firewall cleanup failed")
	cloud.removeAdminErr = errors.New("administrator cleanup failed")
	err := r.Reconcile(t.Context(), "dev", resolvedDatabase{}, values)
	require.ErrorContains(t, err, "firewall cleanup failed")
	require.ErrorContains(t, err, "administrator cleanup failed")
	require.FileExists(t, path)
	require.Len(t, cloud.firewalls, 1)
	require.Len(t, cloud.administrators, 2)

	cloud.removeRuleErr, cloud.removeAdminErr = nil, nil
	cloud.operations = nil
	server, _, err := r.verifyResources(t.Context(), "", resolvedDatabase{}, values)
	require.NoError(t, err)
	server.PublicNetworkAccessEnabled = false
	require.NoError(t, r.Recover(t.Context(), "dev", resolvedDatabase{}, values))
	require.Equal(t, []string{"remove-firewall", "remove-administrator"}, cloud.operations)
	require.NoFileExists(t, path)
	require.Empty(t, cloud.firewalls)
	require.Len(t, cloud.administrators, 1)
}

func TestLocalPostgresBootstrapRejectsUnsafePrerequisitesBeforeMutation(t *testing.T) {
	for _, failure := range []string{"private endpoint", "unowned caller", "firewall collision", "state directory"} {
		t.Run(failure, func(t *testing.T) {
			r, cloud, values, path := newLocalBootstrapFixture(t, localTestCallerID)
			server, _, err := r.verifyResources(t.Context(), "", resolvedDatabase{}, values)
			require.NoError(t, err)
			switch failure {
			case "private endpoint":
				server.PublicNetworkAccessEnabled = false
			case "unowned caller":
				cloud.administrators[server.ServerID+"/administrators/"+localTestCallerID] =
					postgresBootstrapAdministrator{
						PrincipalName: "azd-local-foreign", PrincipalType: "User", TenantID: localTestTenantID,
					}
			case "firewall collision":
				state := postgresLocalBootstrapState{
					ServerID: server.ServerID, Environment: "dev", Suffix: "0123456789abcdef",
				}
				cloud.firewalls[state.firewallName()] = true
			case "state directory":
				blocked := filepath.Join(t.TempDir(), "not-a-directory")
				require.NoError(t, os.WriteFile(blocked, nil, 0o600))
				r.stateDir = func() (string, error) { return blocked, nil }
			}
			require.Error(t, r.Reconcile(t.Context(), "dev", resolvedDatabase{}, values))
			require.Empty(t, cloud.operations)
			require.NoFileExists(t, path)
		})
	}
}

func TestLocalPostgresBootstrapRefusesChangedAndConcurrentOwnership(t *testing.T) {
	r, cloud, values, path := newLocalBootstrapFixture(t, localTestCallerID)
	r.sql = func(ctx context.Context, _ *verifiedPostgresResources, _ azcore.TokenCredential,
		_ resolvedDatabase, _ map[string]string, _ string,
	) error {
		require.ErrorContains(t, r.Reconcile(ctx, "dev", resolvedDatabase{}, values), "in progress")
		require.ErrorContains(t, r.Recover(ctx, "dev", resolvedDatabase{}, values), "in progress")
		for id, administrator := range cloud.administrators {
			if administrator.PrincipalName == "azd-local-0123456789abcdef" {
				administrator.PrincipalName = "changed-administrator"
				cloud.administrators[id] = administrator
			}
		}
		return nil
	}
	require.ErrorContains(t, r.Reconcile(t.Context(), "dev", resolvedDatabase{}, values), "ownership changed")
	require.FileExists(t, path)
	require.Empty(t, cloud.firewalls)
	require.Len(t, cloud.administrators, 2)
}

func TestLocalPostgresBootstrapRefusesForeignRecoveryState(t *testing.T) {
	r, cloud, values, path := newLocalBootstrapFixture(t, localTestCallerID)
	server, _, err := r.verifyResources(t.Context(), "", resolvedDatabase{}, values)
	require.NoError(t, err)
	state := postgresLocalBootstrapState{
		ServerID: server.ServerID, Environment: "foreign-environment", Suffix: "0123456789abcdef",
	}
	data, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	require.ErrorContains(t, r.Recover(t.Context(), "dev", resolvedDatabase{}, values), "ownership")
	require.Empty(t, cloud.operations)
	require.FileExists(t, path)
}

func TestLocalPostgresReadinessRetriesOnlyConnectionFailures(t *testing.T) {
	for _, test := range []struct {
		err   error
		retry bool
	}{
		{err: &postgresConnectionError{cause: &pgconn.PgError{Code: "28000"}}, retry: true},
		{err: &postgresConnectionError{cause: &pgconn.PgError{Code: "28P01"}}, retry: true},
		{err: &postgresConnectionError{cause: io.EOF}, retry: true},
		{err: &postgresConnectionError{cause: errors.New("connection reset by peer")}, retry: true},
		{err: &postgresConnectionError{cause: errors.New("x509: certificate signed by unknown authority")}},
		{err: &postgresConnectionError{cause: context.Canceled}},
		{err: &pgconn.PgError{Code: "28000"}},
		{err: &pgconn.PgError{Code: "42501"}},
		{err: configurationError("role ownership changed")},
	} {
		require.Equal(t, test.retry, isPostgresLocalReadinessError(test.err), "%v", test.err)
	}
}

func TestLocalPostgresBootstrapRetriesPropagationButNotSQLPolicy(t *testing.T) {
	for _, connectionFailure := range []bool{false, true} {
		r, _, values, path := newLocalBootstrapFixture(t, localTestSelectedID)
		calls := 0
		r.sql = func(context.Context, *verifiedPostgresResources, azcore.TokenCredential,
			resolvedDatabase, map[string]string, string,
		) error {
			calls++
			if calls > 1 {
				return nil
			}
			if connectionFailure {
				return &postgresConnectionError{cause: &pgconn.PgError{Code: "28000"}}
			}
			return configurationError("database role ownership changed")
		}
		err := r.Reconcile(t.Context(), "dev", resolvedDatabase{}, values)
		if connectionFailure {
			require.NoError(t, err)
			require.Equal(t, 2, calls)
		} else {
			require.ErrorContains(t, err, "ownership changed")
			require.Equal(t, 1, calls)
		}
		require.NoFileExists(t, path)
	}
}

func TestLocalPostgresAdministratorDeleteVerifiesOwnershipAndEmptySuccess(t *testing.T) {
	for _, scenario := range []string{"success", "changed", "absence-unconfirmed"} {
		t.Run(scenario, func(t *testing.T) {
			id := "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.DBforPostgreSQL/flexibleServers/server/administrators/" + localTestCallerID
			expected := postgresBootstrapAdministrator{
				PrincipalName: "azd-local-0123456789abcdef", PrincipalType: "User", TenantID: localTestTenantID,
			}
			deletions, removed := 0, false
			resources, err := armresources.NewClient("sub", postgresPolicyCredential{}, &arm.ClientOptions{
				ClientOptions: policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1},
					Transport: postgresPolicyTransport(func(request *http.Request) (*http.Response, error) {
						require.Equal(t, id, request.URL.Path)
						status, body := http.StatusOK, ""
						if request.Method == http.MethodDelete {
							deletions++
							removed = true
							status = http.StatusNoContent
						} else {
							require.Equal(t, http.MethodGet, request.Method)
							if removed && scenario != "absence-unconfirmed" {
								status, body = http.StatusNotFound, `{"error":{"code":"ResourceNotFound"}}`
							} else {
								name := expected.PrincipalName
								if scenario == "changed" {
									name = "persistent-user"
								}
								data, err := json.Marshal(map[string]any{
									"id": id, "properties": map[string]string{
										"objectId": localTestCallerID, "principalName": name,
										"principalType": "User", "tenantId": localTestTenantID,
									},
								})
								require.NoError(t, err)
								body = string(data)
							}
						}
						return &http.Response{StatusCode: status,
							Header: http.Header{"Content-Type": {"application/json"}},
							Body:   io.NopCloser(strings.NewReader(body)), Request: request}, nil
					})},
			})
			require.NoError(t, err)
			cloud := &azurePostgresLocalCloud{resources: resources}
			err = cloud.removeAdministrator(t.Context(), id, expected)
			if scenario == "success" {
				require.NoError(t, err)
				require.Equal(t, 1, deletions)
			} else {
				require.Error(t, err)
				if scenario == "changed" {
					require.Zero(t, deletions)
				} else {
					require.Equal(t, 1, deletions)
				}
			}
		})
	}
}
