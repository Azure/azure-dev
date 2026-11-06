// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPostgresBootstrapInputRoundTrip(t *testing.T) {
	input := testPostgresBootstrapInput()

	encoded, err := encodePostgresBootstrapInput(input)
	require.NoError(t, err)
	require.NotContains(t, encoded, input.Host)

	actual, err := decodePostgresBootstrapInput(encoded)
	require.NoError(t, err)
	require.Equal(t, input, actual)
}

func TestPostgresBootstrapInputRejectsUnknownFields(t *testing.T) {
	encoded, err := encodePostgresBootstrapInput(testPostgresBootstrapInput())
	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	raw = []byte(strings.TrimSuffix(string(raw), "}") + `,"password":"secret"}`)

	_, err = decodePostgresBootstrapInput(base64.RawURLEncoding.EncodeToString(raw))
	require.ErrorContains(t, err, "unknown field")
	require.NotContains(t, err.Error(), "secret")
}

func TestPostgresBootstrapInputRequiresSeparateAdministrators(t *testing.T) {
	input := testPostgresBootstrapInput()
	input.ExpectedAdministratorName = ""

	_, err := encodePostgresBootstrapInput(input)
	require.ErrorContains(t, err, "expected PostgreSQL administrator")

	input = testPostgresBootstrapInput()
	input.SessionAdministratorName = ""
	_, err = encodePostgresBootstrapInput(input)
	require.ErrorContains(t, err, "session PostgreSQL administrator")
}

func TestVerifiedPostgresResourcesSessionAdministrator(t *testing.T) {
	resources := &verifiedPostgresResources{AdministratorName: "developer"}
	require.Equal(t, "developer", resources.sessionAdministratorName())

	resources.SessionAdministratorName = "bootstrap-job"
	require.Equal(t, "bootstrap-job", resources.sessionAdministratorName())
	require.Equal(t, "developer", resources.AdministratorName)
}

func testPostgresBootstrapInput() postgresBootstrapInput {
	return postgresBootstrapInput{
		Version: postgresBootstrapProtocolVersion,
		ServerResourceID: "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/" +
			"providers/Microsoft.DBforPostgreSQL/flexibleServers/server",
		Host:                      "server.postgres.database.azure.com",
		DatabaseName:              "petclinic",
		ApplicationUsername:       "petclinic_app",
		ApplicationPrincipalID:    "00000000-0000-0000-0000-000000000002",
		EnvironmentName:           "dev",
		ExpectedAdministratorName: "developer",
		SessionAdministratorName:  "bootstrap_job",
	}
}
