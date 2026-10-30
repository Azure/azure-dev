// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolvePostgreSQLPassword(t *testing.T) {
	const valid = "Valid-Pass9!"
	tests := []struct {
		name      string
		values    map[string]string
		process   map[string]string
		want      string
		persisted bool
		wantErr   string
	}{
		{
			name:   "persisted wins",
			values: map[string]string{databasePasswordEnv: valid},
			want:   valid,
		},
		{
			name:    "matching provided value",
			values:  map[string]string{databasePasswordEnv: valid},
			process: map[string]string{databasePasswordEnv: valid},
			want:    valid,
		},
		{
			name:    "conflicting provided value",
			values:  map[string]string{databasePasswordEnv: valid},
			process: map[string]string{databasePasswordEnv: "Other-Pass8!"},
			wantErr: "conflicts",
		},
		{
			name:    "explicit empty value",
			process: map[string]string{databasePasswordEnv: ""},
			wantErr: "explicitly empty",
		},
		{
			name:      "provided new value",
			process:   map[string]string{databasePasswordEnv: valid},
			want:      valid,
			persisted: true,
		},
		{
			name:    "existing resource has no password",
			values:  map[string]string{postgresServerIDEnv: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg"},
			wantErr: "existing infrastructure",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			password, persist, err := resolvePostgreSQLPassword(
				test.values,
				func(key string) (string, bool) {
					value, ok := test.process[key]
					return value, ok
				},
				func() error { return nil },
			)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				require.NotContains(t, err.Error(), valid)
				return
			}
			require.NoError(t, err)
			if test.want == "" {
				require.NoError(t, validatePostgreSQLPassword(password))
				require.Len(t, password, generatedPasswordLength)
				require.True(t, persist)
			} else {
				require.Equal(t, test.want, password)
				require.Equal(t, test.persisted, persist)
			}
		})
	}
}

func TestResolvePostgreSQLPasswordChecksDeploymentOnlyBeforeGeneration(t *testing.T) {
	const supplied = "Valid-Pass9!"
	checkCalls := 0
	check := func() error {
		checkCalls++
		return errors.New("existing deployment")
	}

	password, persist, err := resolvePostgreSQLPassword(
		map[string]string{},
		func(key string) (string, bool) {
			require.Equal(t, databasePasswordEnv, key)
			return supplied, true
		},
		check,
	)
	require.NoError(t, err)
	require.Equal(t, supplied, password)
	require.True(t, persist)
	require.Zero(t, checkCalls)

	_, _, err = resolvePostgreSQLPassword(
		map[string]string{},
		func(string) (string, bool) { return "", false },
		check,
	)
	require.ErrorContains(t, err, "existing deployment")
	require.Equal(t, 1, checkCalls)
}

func TestGeneratedPasswordMeetsAzureRequirements(t *testing.T) {
	password, err := generatePostgreSQLPassword()
	require.NoError(t, err)
	require.NoError(t, validatePostgreSQLPassword(password))
	require.NotContains(t, password, `"`)
	require.NotContains(t, password, `'`)
	require.NotContains(t, password, `\`)
}
