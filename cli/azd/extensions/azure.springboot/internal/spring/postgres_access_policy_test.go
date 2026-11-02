// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

type postgresPolicyRow struct {
	values []any
	err    error
}

func (r postgresPolicyRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(destinations) != len(r.values) {
		return errors.New("unexpected PostgreSQL policy result column count")
	}
	for i, destination := range destinations {
		reflect.ValueOf(destination).Elem().Set(reflect.ValueOf(r.values[i]))
	}
	return nil
}

type postgresPolicyRows struct {
	pgx.Rows
	values [][]any
	index  int
	err    error
}

func (r *postgresPolicyRows) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}

func (r *postgresPolicyRows) Scan(destinations ...any) error {
	return (postgresPolicyRow{values: r.values[r.index-1]}).Scan(destinations...)
}

func (r *postgresPolicyRows) Close()     {}
func (r *postgresPolicyRows) Err() error { return r.err }

type postgresPolicyTx struct {
	pgx.Tx
	row   func(string, ...any) pgx.Row
	rows  func(string, ...any) (pgx.Rows, error)
	execs []string
}

func (tx *postgresPolicyTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	return tx.row(query, args...)
}

func (tx *postgresPolicyTx) Query(_ context.Context, query string, args ...any) (pgx.Rows, error) {
	return tx.rows(query, args...)
}

func (tx *postgresPolicyTx) Exec(_ context.Context, statement string, _ ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, statement)
	return pgconn.NewCommandTag(""), nil
}

func applicationPolicyTx(t *testing.T, objects, schemas [][]any) *postgresPolicyTx {
	t.Helper()
	return &postgresPolicyTx{
		row: func(query string, args ...any) pgx.Row {
			switch {
			case strings.Contains(query, "rolcanlogin"):
				return postgresPolicyRow{values: []any{true, false, false, false, false, false, "marker"}}
			case strings.Contains(query, "pg_auth_members"):
				return postgresPolicyRow{values: []any{0}}
			case strings.Contains(query, "shobj_description(oid, 'pg_database')"):
				return postgresPolicyRow{values: []any{"developer", "marker", true}}
			case strings.Contains(query, "SELECT pg_get_userbyid(datdba)"):
				return postgresPolicyRow{values: []any{"developer"}}
			case strings.Contains(query, "SELECT has_database_privilege"):
				privilege := strings.Contains(query, "'CONNECT'")
				require.Equal(t, []any{"app", "petclinic"}, args)
				return postgresPolicyRow{values: []any{privilege}}
			case strings.Contains(query, "SELECT has_schema_privilege"):
				require.Equal(t, []any{"app"}, args)
				return postgresPolicyRow{values: []any{true}}
			case strings.Contains(query, "SELECT count(*)"):
				return postgresPolicyRow{values: []any{0}}
			default:
				t.Fatalf("unexpected policy query: %s", query)
				return nil
			}
		},
		rows: func(query string, _ ...any) (pgx.Rows, error) {
			switch {
			case strings.Contains(query, "FROM pg_class c"):
				require.Contains(t, query, "pg_proc")
				require.Contains(t, query, "pg_type")
				return &postgresPolicyRows{values: objects}, nil
			case strings.Contains(query, "SELECT nspname"):
				return &postgresPolicyRows{values: schemas}, nil
			default:
				t.Fatalf("unexpected policy rows query: %s", query)
				return nil, nil
			}
		},
	}
}

func TestPostgresFixedPolicyPreservesApplicationOwnedObjects(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "app-owned-rerun"}[populated], func(t *testing.T) {
			var objects [][]any
			if populated {
				objects = [][]any{
					{"public", "items", "app", "r"},
					{"public", "items_id_seq", "app", "S"},
					{"public", "item_view", "app", "v"},
					{"public", "item_function", "app", "function"},
					{"public", "item_type", "app", "type"},
				}
			}
			tx := applicationPolicyTx(t, objects, [][]any{{"public", "pg_database_owner", true, true}})
			err := reconcileApplicationPrivileges(t.Context(), tx, resolvedDatabase{
				Name: "petclinic", Username: "app",
			}, "developer", "marker")
			require.NoError(t, err)
			require.Equal(t, []string{
				`REVOKE ALL ON DATABASE "petclinic" FROM "app"`,
				`GRANT CONNECT ON DATABASE "petclinic" TO "app"`,
				`REVOKE CREATE, TEMPORARY ON DATABASE "petclinic" FROM PUBLIC`,
				`REVOKE CREATE ON SCHEMA public FROM PUBLIC`,
				`REVOKE ALL ON SCHEMA public FROM "app"`,
				`GRANT USAGE, CREATE ON SCHEMA public TO "app"`,
			}, tx.execs)
			for _, statement := range tx.execs {
				require.NotContains(t, statement, "OWNER")
				require.NotContains(t, statement, "ON ALL TABLES")
				require.NotContains(t, statement, "ON ALL SEQUENCES")
				require.NotContains(t, statement, "DEFAULT PRIVILEGES")
			}
		})
	}
}

func TestPostgresFixedPolicyRejectsApplicationObjectAdoption(t *testing.T) {
	for _, test := range []struct {
		name    string
		objects [][]any
		schemas [][]any
	}{
		{name: "administrator-table", objects: [][]any{{"public", "items", "developer", "r"}}},
		{name: "foreign-table", objects: [][]any{{"public", "items", "foreign", "r"}}},
		{name: "foreign-sequence", objects: [][]any{{"public", "items_id_seq", "foreign", "S"}}},
		{name: "administrator-function", objects: [][]any{{"public", "routine", "developer", "function"}}},
		{name: "foreign-type", objects: [][]any{{"public", "enum", "foreign", "type"}}},
		{name: "app-nonpublic-table", objects: [][]any{{"other", "items", "app", "r"}}},
		{name: "app-public-schema", schemas: [][]any{{"public", "app", false, true}}},
		{name: "foreign-public-schema", schemas: [][]any{{"public", "foreign", false, true}}},
		{name: "unverified-platform-schema", schemas: [][]any{{"public", "azure_pg_admin", false, true}}},
		{name: "app-extra-schema", schemas: [][]any{{"other", "app", false, true}}},
		{name: "administrator-extra-schema", schemas: [][]any{{"other", "developer", true, false}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := applicationPolicyTx(t, test.objects, test.schemas)
			err := reconcileApplicationPrivileges(t.Context(), tx, resolvedDatabase{
				Name: "petclinic", Username: "app",
			}, "developer", "marker")
			require.Error(t, err)
			require.Empty(t, tx.execs, "unexpected objects must be rejected before grant changes")
		})
	}
}

func TestPostgresBookkeepingIsAdministratorOnlyAndExactlyScoped(t *testing.T) {
	for _, test := range []struct {
		name          string
		object        []any
		schema        []any
		expectedError bool
	}{
		{
			name:   "verified-receipt",
			object: []any{"azd_initialization", "receipt", "developer", "r"},
			schema: []any{"azd_initialization", "developer", true, false},
		},
		{
			name:   "verified-baseline-file",
			object: []any{"azd_initialization", "baseline_file", "developer", "r"},
			schema: []any{"azd_initialization", "developer", true, false},
		},
		{
			name:   "verified-baseline-object",
			object: []any{"azd_initialization", "baseline_object", "developer", "r"},
			schema: []any{"azd_initialization", "developer", true, false},
		},
		{
			name: "foreign-receipt", object: []any{"azd_initialization", "receipt", "foreign", "r"},
			expectedError: true,
		},
		{
			name: "app-receipt", object: []any{"azd_initialization", "receipt", "app", "r"},
			expectedError: true,
		},
		{
			name: "wrong-name", object: []any{"azd_initialization", "items", "developer", "r"},
			expectedError: true,
		},
		{
			name: "wrong-kind", object: []any{"azd_initialization", "receipt", "developer", "v"},
			expectedError: true,
		},
		{
			name: "wrong-schema", object: []any{"public", "receipt", "developer", "r"},
			expectedError: true,
		},
		{
			name: "app-schema-access", schema: []any{"azd_initialization", "developer", true, true},
			expectedError: true,
		},
		{
			name: "foreign-schema", schema: []any{"azd_initialization", "foreign", false, false},
			expectedError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var objects, schemas [][]any
			if test.object != nil {
				objects = append(objects, test.object)
			}
			if test.schema != nil {
				schemas = append(schemas, test.schema)
			}
			tx := applicationPolicyTx(t, objects, schemas)
			err := verifyApplicationObjectOwnership(t.Context(), tx, "app", "developer")
			if test.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, tx.execs)
		})
	}
}

func TestPostgresFixedPolicyRequiresSchemaCreateButNoDatabaseElevation(t *testing.T) {
	queries := []string{
		"SELECT has_database_privilege($1, $2, 'CONNECT')",
		"SELECT has_database_privilege($1, $2, 'TEMPORARY')",
		"SELECT has_database_privilege($1, $2, 'CREATE')",
		"SELECT has_schema_privilege($1, 'public', 'USAGE')",
		"SELECT has_schema_privilege($1, 'public', 'CREATE')",
	}
	expected := []bool{true, false, false, true, true}
	for failed := -1; failed < len(queries); failed++ {
		name := "fixed-policy"
		if failed >= 0 {
			name = queries[failed]
		}
		t.Run(name, func(t *testing.T) {
			index := 0
			tx := &postgresPolicyTx{row: func(query string, _ ...any) pgx.Row {
				require.Equal(t, queries[index], query)
				result := expected[index]
				if index == failed {
					result = !result
				}
				index++
				return postgresPolicyRow{values: []any{result}}
			}}
			err := verifyEffectiveApplicationAccess(t.Context(), tx, "app", "petclinic")
			if failed < 0 {
				require.NoError(t, err)
				require.Equal(t, len(queries), index)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPostgresFixedPolicyRejectsDatabaseOwnership(t *testing.T) {
	tx := &postgresPolicyTx{row: func(string, ...any) pgx.Row {
		return postgresPolicyRow{values: []any{"app"}}
	}}
	require.ErrorContains(t, verifyApplicationObjectOwnership(
		t.Context(), tx, "app", "developer",
	), "application role owns the target database")
}

func TestPostgresDatabaseScopeDoesNotAdoptUnmarkedObjects(t *testing.T) {
	for _, test := range []struct {
		name        string
		comment     string
		objects     int
		claim       bool
		expectedErr string
	}{
		{name: "fresh", claim: true},
		{name: "marked-rerun", comment: "marker", objects: 4},
		{name: "unmarked-objects", objects: 1, expectedErr: "contains unowned application objects"},
		{name: "foreign-marker", comment: "old-policy-marker", expectedErr: "existing ownership marker"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &postgresPolicyTx{row: func(query string, _ ...any) pgx.Row {
				if strings.Contains(query, "shobj_description") {
					return postgresPolicyRow{values: []any{"developer", test.comment, true}}
				}
				require.Contains(t, query, "SELECT count(*) FROM application_objects")
				return postgresPolicyRow{values: []any{test.objects}}
			}}
			claim, err := validateOwnedDatabaseScope(t.Context(), tx, "petclinic", "developer", "marker")
			if test.expectedErr == "" {
				require.NoError(t, err)
				require.Equal(t, test.claim, claim)
			} else {
				require.ErrorContains(t, err, test.expectedErr)
				require.False(t, claim)
			}
			require.Empty(t, tx.execs)
		})
	}
}

func TestPostgresFixedPolicyRejectsElevatedRolesAndMemberships(t *testing.T) {
	for _, attribute := range []string{
		"ordinary", "no-login", "superuser", "createdb", "createrole", "replication", "bypassrls", "marker", "membership",
	} {
		t.Run(attribute, func(t *testing.T) {
			values := []any{true, false, false, false, false, false, "marker"}
			switch attribute {
			case "no-login":
				values[0] = false
			case "superuser":
				values[1] = true
			case "createdb":
				values[2] = true
			case "createrole":
				values[3] = true
			case "replication":
				values[4] = true
			case "bypassrls":
				values[5] = true
			case "marker":
				values[6] = "foreign"
			}
			tx := &postgresPolicyTx{row: func(query string, _ ...any) pgx.Row {
				if strings.Contains(query, "pg_auth_members") {
					count := 0
					if attribute == "membership" {
						count = 1
					}
					return postgresPolicyRow{values: []any{count}}
				}
				return postgresPolicyRow{values: values}
			}}
			err := verifyRoleIsOrdinary(t.Context(), tx, "app", "marker")
			if attribute == "ordinary" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPostgresObjectAccessChecksOwnedTablesAndSequences(t *testing.T) {
	for _, inaccessible := range []int{0, 1} {
		tx := &postgresPolicyTx{row: func(query string, args ...any) pgx.Row {
			require.Equal(t, []any{"app"}, args)
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "USAGE"} {
				require.Contains(t, query, "'"+privilege+"'")
			}
			require.NotContains(t, query, "'TRUNCATE'", "ownership permits operations beyond DML")
			return postgresPolicyRow{values: []any{inaccessible}}
		}}
		err := verifyEffectiveObjectPrivileges(t.Context(), tx, "app")
		if inaccessible == 0 {
			require.NoError(t, err)
		} else {
			require.ErrorContains(t, err, "lacks read/write or sequence access")
		}
	}
}

type postgresPolicyTransport func(*http.Request) (*http.Response, error)

func (f postgresPolicyTransport) Do(request *http.Request) (*http.Response, error) { return f(request) }

type postgresPolicyCredential struct{}

func (postgresPolicyCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "offline-test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestPostgresAuthoredAllPublicFirewallBypassesTemporaryAccess(t *testing.T) {
	for _, result := range []error{nil, errors.New("SQL failed"), context.Canceled} {
		name := "success"
		if result != nil {
			name = result.Error()
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(postgresBootstrapClientIPEnv, "not-an-address")
			requests := 0
			firewalls, err := armpostgresqlflexibleservers.NewFirewallRulesClient(
				"sub", postgresPolicyCredential{}, &arm.ClientOptions{
					ClientOptions: policy.ClientOptions{Transport: postgresPolicyTransport(
						func(request *http.Request) (*http.Response, error) {
							requests++
							require.Equal(t, http.MethodGet, request.Method)
							require.Contains(t, request.URL.Path, "/resourceGroups/rg/")
							require.Contains(t, request.URL.Path, "/flexibleServers/server/firewallRules")
							return &http.Response{
								StatusCode: http.StatusOK,
								Header:     http.Header{"Content-Type": []string{"application/json"}},
								Body: io.NopCloser(strings.NewReader(`{"value":[{"name":"bootstrap-local-ip",` +
									`"properties":{"startIpAddress":"0.0.0.0","endIpAddress":"255.255.255.255"}}]}`)),
								Request: request,
							}, nil
						},
					)},
				},
			)
			require.NoError(t, err)
			// Nil IP, clock and random providers also guard against any temporary-firewall path.
			reconciler := &postgresAccessReconciler{}
			sqlCalls := 0
			err = reconciler.withBootstrapFirewall(t.Context(), firewalls, &verifiedPostgresResources{
				SubscriptionID: "sub", ResourceGroup: "rg", ServerName: "server",
			}, "env", func(context.Context) error {
				sqlCalls++
				return result
			})
			require.ErrorIs(t, err, result)
			require.Equal(t, 1, sqlCalls)
			require.Equal(t, 1, requests, "authored rules must not be changed, deleted or recovered")
		})
	}
}

func TestPostgresFirewallPolicyReadFailureStopsBootstrap(t *testing.T) {
	readFailure := errors.New("policy read failed")
	firewalls, err := armpostgresqlflexibleservers.NewFirewallRulesClient(
		"sub", postgresPolicyCredential{}, &arm.ClientOptions{
			ClientOptions: policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: postgresPolicyTransport(func(*http.Request) (*http.Response, error) {
					return nil, readFailure
				}),
			},
		},
	)
	require.NoError(t, err)
	reconciler := &postgresAccessReconciler{}
	err = reconciler.withBootstrapFirewall(t.Context(), firewalls, &verifiedPostgresResources{
		ResourceGroup: "rg", ServerName: "server",
	}, "env", func(context.Context) error {
		t.Fatal("SQL must not run when the authored firewall policy cannot be verified")
		return nil
	})
	require.ErrorContains(t, err, readFailure.Error())
}

func TestPostgresNarrowFirewallRetainsOwnedTemporaryAccessAndCleanup(t *testing.T) {
	for _, test := range []struct {
		name         string
		createFails  bool
		sqlFails     bool
		cancelSQL    bool
		cleanupFails bool
	}{
		{name: "success"},
		{name: "ambiguous-create", createFails: true},
		{name: "SQL-failure", sqlFails: true},
		{name: "cancellation", cancelSQL: true},
		{name: "cleanup-failure", cleanupFails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(postgresBootstrapClientIPEnv, "8.8.8.8")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			resources := &verifiedPostgresResources{
				ResourceGroup: "rg", ServerName: "server",
				ServerID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/server",
			}
			now := time.Unix(1_700_000_000, 0)
			ruleName := bootstrapFirewallRuleName(
				bootstrapFirewallRulePrefix("env", resources.ServerID), now, "1111111111111111",
			)
			var methods []string
			firewalls, err := armpostgresqlflexibleservers.NewFirewallRulesClient(
				"sub", postgresPolicyCredential{}, &arm.ClientOptions{
					ClientOptions: policy.ClientOptions{
						Retry: policy.RetryOptions{MaxRetries: -1},
						Transport: postgresPolicyTransport(func(request *http.Request) (*http.Response, error) {
							methods = append(methods, request.Method)
							status := http.StatusOK
							body := `{}`
							switch request.Method {
							case http.MethodGet:
								body = `{"value":[{"name":"customer-rule","properties":` +
									`{"startIpAddress":"1.1.1.1","endIpAddress":"1.1.1.1"}}]}`
							case http.MethodPut:
								require.True(t, strings.HasSuffix(request.URL.Path, "/firewallRules/"+ruleName))
								var rule armpostgresqlflexibleservers.FirewallRule
								require.NoError(t, json.NewDecoder(request.Body).Decode(&rule))
								require.NotNil(t, rule.Properties)
								require.Equal(t, "8.8.8.8", *rule.Properties.StartIPAddress)
								require.Equal(t, "8.8.8.8", *rule.Properties.EndIPAddress)
								body = `{"name":"` + ruleName + `","properties":` +
									`{"startIpAddress":"8.8.8.8","endIpAddress":"8.8.8.8"}}`
								if test.createFails {
									status = http.StatusBadRequest
									body = `{"error":{"code":"CreateFailed","message":"create failed"}}`
								}
							case http.MethodDelete:
								require.NoError(t, request.Context().Err(), "cleanup must survive cancellation")
								require.True(t, strings.HasSuffix(request.URL.Path, "/firewallRules/"+ruleName))
								if test.cleanupFails {
									status = http.StatusBadRequest
									body = `{"error":{"code":"CleanupFailed","message":"cleanup failed"}}`
								}
							default:
								t.Fatalf("unexpected firewall method %s", request.Method)
							}
							return &http.Response{
								StatusCode: status,
								Header:     http.Header{"Content-Type": []string{"application/json"}},
								Body:       io.NopCloser(strings.NewReader(body)),
								Request:    request,
							}, nil
						}),
					},
				},
			)
			require.NoError(t, err)
			reconciler := &postgresAccessReconciler{
				now:          func() time.Time { return now },
				randomSuffix: func() (string, error) { return "1111111111111111", nil },
			}
			sqlCalls := 0
			err = reconciler.withBootstrapFirewall(ctx, firewalls, resources, "env", func(context.Context) error {
				sqlCalls++
				if test.cancelSQL {
					cancel()
					return ctx.Err()
				}
				if test.sqlFails {
					return errors.New("SQL failed")
				}
				return nil
			})
			if test.createFails || test.sqlFails || test.cancelSQL || test.cleanupFails {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if test.createFails {
				require.Equal(t, 0, sqlCalls)
				require.ErrorContains(t, err, "creating an exact owned")
			} else {
				require.Equal(t, 1, sqlCalls)
			}
			if test.sqlFails {
				require.ErrorContains(t, err, "SQL failed")
			}
			if test.cancelSQL {
				require.ErrorIs(t, err, context.Canceled)
			}
			if test.cleanupFails {
				require.ErrorContains(t, err, "deleting the exact owned")
			}
			require.Equal(t, []string{http.MethodGet, http.MethodGet, http.MethodPut, http.MethodDelete}, methods)
		})
	}
}
