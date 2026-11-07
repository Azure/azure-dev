// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const (
	postgresBootstrapProtocolVersion = 1
	postgresBootstrapInputEnv        = "AZD_SPRING_POSTGRES_BOOTSTRAP_INPUT"
)

type postgresBootstrapInput struct {
	Version                   int    `json:"version"`
	ServerResourceID          string `json:"serverResourceId"`
	Host                      string `json:"host"`
	DatabaseName              string `json:"databaseName"`
	ApplicationUsername       string `json:"applicationUsername"`
	ApplicationPrincipalID    string `json:"applicationPrincipalId"`
	EnvironmentName           string `json:"environmentName"`
	ExpectedAdministratorName string `json:"expectedAdministratorName"`
	SessionAdministratorName  string `json:"sessionAdministratorName"`
}

func (i postgresBootstrapInput) validate() error {
	if i.Version != postgresBootstrapProtocolVersion {
		return fmt.Errorf("unsupported PostgreSQL bootstrap protocol version %d", i.Version)
	}
	if _, err := parsePostgresServerResourceID(i.ServerResourceID); err != nil {
		return fmt.Errorf("invalid PostgreSQL server resource ID: %w", err)
	}
	if i.Host == "" || strings.TrimSpace(i.Host) != i.Host ||
		strings.ContainsAny(i.Host, " \t\r\n/\\:@?#") {
		return fmt.Errorf("invalid PostgreSQL server host")
	}
	if !databaseIdentifierPattern.MatchString(i.DatabaseName) {
		return fmt.Errorf("invalid PostgreSQL database name")
	}
	if !databaseLoginPattern.MatchString(i.ApplicationUsername) {
		return fmt.Errorf("invalid PostgreSQL application username")
	}
	if !databasePrincipalIDPattern.MatchString(i.ApplicationPrincipalID) {
		return fmt.Errorf("invalid PostgreSQL application principal ID")
	}
	if strings.TrimSpace(i.EnvironmentName) == "" {
		return fmt.Errorf("invalid azd environment name")
	}
	if !validAdministratorName(i.ExpectedAdministratorName) {
		return fmt.Errorf("invalid expected PostgreSQL administrator name")
	}
	if !databaseLoginPattern.MatchString(i.SessionAdministratorName) {
		return fmt.Errorf("invalid session PostgreSQL administrator name")
	}
	return nil
}

func parsePostgresServerResourceID(value string) (*arm.ResourceID, error) {
	resourceID, err := arm.ParseResourceID(value)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(resourceID.ResourceType.String(), "Microsoft.DBforPostgreSQL/flexibleServers") {
		return nil, fmt.Errorf("resource type is %q", resourceID.ResourceType.String())
	}
	return resourceID, nil
}

func encodePostgresBootstrapInput(input postgresBootstrapInput) (string, error) {
	if err := input.validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encoding PostgreSQL bootstrap input: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePostgresBootstrapInput(value string) (postgresBootstrapInput, error) {
	if len(value) > 16<<10 {
		return postgresBootstrapInput{}, fmt.Errorf("decoding PostgreSQL bootstrap input: value is too large")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return postgresBootstrapInput{}, fmt.Errorf("decoding PostgreSQL bootstrap input: %w", err)
	}
	var input postgresBootstrapInput
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return postgresBootstrapInput{}, fmt.Errorf("decoding PostgreSQL bootstrap input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return postgresBootstrapInput{}, fmt.Errorf("decoding PostgreSQL bootstrap input: trailing content")
	}
	if err := input.validate(); err != nil {
		return postgresBootstrapInput{}, err
	}
	return input, nil
}

func RunPostgresBootstrap(
	ctx context.Context,
	encodedInput string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	input, err := decodePostgresBootstrapInput(encodedInput)
	if err != nil {
		return writePostgresBootstrapFailure(stderr, err)
	}

	credential, err := azidentity.NewManagedIdentityCredential(nil)
	if err != nil {
		return writePostgresBootstrapFailure(
			stderr,
			fmt.Errorf("creating the bootstrap managed identity credential: %w", err),
		)
	}
	token, err := credential.GetToken(
		ctx,
		policy.TokenRequestOptions{Scopes: []string{postgresTokenScope}},
	)
	if err != nil {
		return writePostgresBootstrapFailure(
			stderr,
			fmt.Errorf("acquiring the PostgreSQL bootstrap access token: %w", err),
		)
	}

	resources := &verifiedPostgresResources{
		ServerID:                 input.ServerResourceID,
		Host:                     input.Host,
		AdministratorName:        input.ExpectedAdministratorName,
		SessionAdministratorName: input.SessionAdministratorName,
		IdentityPrincipalID:      strings.ToLower(input.ApplicationPrincipalID),
	}
	database := resolvedDatabase{
		Type:             databasePostgreSQL,
		ServerResourceID: input.ServerResourceID,
		Name:             input.DatabaseName,
		Authentication:   databaseAuthenticationManagedIdentity,
		Username:         input.ApplicationUsername,
	}
	reconciler := newPostgresAccessReconciler(nil)
	if err := reconciler.reconcileSQLWithAdministratorPassword(
		ctx,
		resources,
		database,
		map[string]string{},
		input.EnvironmentName,
		token.Token,
	); err != nil {
		return writePostgresBootstrapFailure(stderr, err)
	}
	if err := json.NewEncoder(stdout).Encode(map[string]string{"status": "succeeded"}); err != nil {
		return fmt.Errorf("writing PostgreSQL bootstrap success output: %w", err)
	}
	return nil
}

func writePostgresBootstrapFailure(writer io.Writer, err error) error {
	safe := nonDisclosureError(err)
	if encodeErr := json.NewEncoder(writer).Encode(map[string]string{
		"status": "failed",
		"error":  safe.Error(),
	}); encodeErr != nil {
		return errors.Join(safe, fmt.Errorf("writing failure output: %w", encodeErr))
	}
	return safe
}
