// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Delta456/box-cli-maker/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/microsoft/azure-devops-go-api/azuredevops"
)

// helper method to verify that a configuration exists in the .env file or in system environment variables
func ensureConfigExists(ctx context.Context, env *environment.Environment, key string, label string) (string, error) {
	value := env.Values[key]
	if value != "" {
		return value, nil
	}

	value, exists := os.LookupEnv(key)
	if !exists || value == "" {
		return value, fmt.Errorf("%s not found in environment variable %s", label, key)
	}
	return value, nil
}

// helper method to ensure an Azure DevOps PAT exists either in .env or system environment variables
func EnsurePatExists(ctx context.Context, env *environment.Environment, console input.Console) (string, error) {
	value, err := ensureConfigExists(ctx, env, AzDoPatName, "azure devops personal access token")
	if err != nil {
		console.Message(ctx, fmt.Sprintf(
			"You need an %s. Please create a PAT by following the instructions here %s",
			output.WithWarningFormat("Azure DevOps Personal Access Token (PAT)"),
			output.WithLinkFormat("https://aka.ms/azure-dev/azdo-pat")))
		console.Message(ctx, fmt.Sprintf("(%s this prompt by setting the PAT to env var: %s)",
			output.WithWarningFormat("%s", "skip"),
			output.WithHighLightFormat("%s", AzDoPatName)))

		pat, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Personal Access Token (PAT):",
			DefaultValue: "",
		})
		if err != nil {
			return "", fmt.Errorf("asking for pat: %w", err)
		}
		// set the pat as an environment variable for this cmd run
		// note: the scope of this env var is only this shell invocation and won't be available in the caller parent shell
		os.Setenv(AzDoPatName, pat)
		value = pat
	}
	return value, nil
}

func getUserProfileId(ctx context.Context, pat string) (id string, err error) {
	req, err := withAuthRequest(
		ctx, "https://app.vssps.visualstudio.com/_apis/profile/profiles/me?api-version=5.1", pat)
	if err != nil {
		return id, fmt.Errorf("getting user profile: %w", err)
	}

	response, err := sendRequest(req)
	if err != nil {
		return id, fmt.Errorf("getting user profile: %w", err)
	}

	type azdoProfile struct {
		DisplayName  string `json:"displayName"`
		PublicAlias  string `json:"publicAlias"`
		EmailAddress string `json:"emailAddress"`
		CoreRevision string `json:"coreRevision"`
		TimeStamp    string `json:"timeStamp"`
		Id           string `json:"id"`
		Revision     string `json:"revision"`
	}
	var responseValue azdoProfile
	err = json.Unmarshal(response, &responseValue)
	if err != nil {
		return id, fmt.Errorf("parsing response: %w", err)
	}
	id = responseValue.Id

	return id, nil
}

func sendRequest(req *http.Request) (responseBody []byte, err error) {
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return responseBody, fmt.Errorf("sending http request: %w", err)
	}
	if response != nil && response.Body != nil {
		if response.StatusCode == 401 {
			// unauthorized
			return responseBody, fmt.Errorf("unauthorized PAT")
		}

		var err error
		defer func() {
			if closeError := response.Body.Close(); closeError != nil {
				err = closeError
			}
		}()
		responseBody, err := io.ReadAll(response.Body)
		if err != nil {
			return responseBody, fmt.Errorf("reading the response body: %w", err)
		}
		return bytes.TrimPrefix(responseBody, []byte("\xef\xbb\xbf")), nil
	}
	return responseBody, fmt.Errorf("invalid response, nil")
}

func withAuthRequest(ctx context.Context, url, pat string) (request *http.Request, err error) {
	req, err := http.NewRequest(
		http.MethodGet,
		url,
		nil)
	if err != nil {
		return nil, fmt.Errorf("creating http request: %w", err)
	}
	// add context
	req = req.WithContext(ctx)
	// auth
	req.Header.Add("Authorization", azuredevops.CreateBasicAuthHeaderValue("", pat))
	req.Header.Add("Accept", "application/json;api-version=5.1")
	return req, nil
}

func getUserOrganizations(ctx context.Context, pat string) (organizations []string, err error) {
	memberId, err := getUserProfileId(ctx, pat)
	if err != nil {
		return organizations, fmt.Errorf("getting organization list: %w", err)
	}

	req, err := withAuthRequest(
		ctx,
		fmt.Sprintf("https://app.vssps.visualstudio.com/_apis/accounts?memberId=%s&api-version=5.1", memberId),
		pat)
	if err != nil {
		return organizations, fmt.Errorf("getting organization list: %w", err)
	}

	response, err := sendRequest(req)
	if err != nil {
		return organizations, fmt.Errorf("getting organization list: %w", err)
	}

	type azdoOrganization struct {
		Name string `json:"accountName"`
	}
	type azdoOrganizationResponse struct {
		Total         int                `json:"count"`
		Organizations []azdoOrganization `json:"value"`
	}
	var responseValue azdoOrganizationResponse
	err = json.Unmarshal(response, &responseValue)
	if err != nil {
		return organizations, fmt.Errorf("parsing response: %w", err)
	}

	for _, orgName := range responseValue.Organizations {
		organizations = append(organizations, orgName.Name)
	}

	return organizations, nil
}

func pickOrganizationFromList(ctx context.Context, organizationsList []string, console input.Console) (orgName string) {
	if len(organizationsList) > 0 {
		// At least one organization to display in the list
		// Adding extra option for creating a new org
		organizationsList = append(organizationsList, "Create a new Organization")
		azdoOrgIndex, _ := console.Select(ctx, input.ConsoleOptions{
			Message:      "Select the Azure DevOps organization",
			Options:      organizationsList,
			DefaultValue: organizationsList[0],
		})
		if azdoOrgIndex != len(organizationsList)-1 {
			orgName = organizationsList[azdoOrgIndex]
		}
	} else {
		console.Message(ctx, "No organizations found within Azure DevOps")
	}

	if orgName == "" {
		// Either no organization found or the customer selected `Create a new Organization`
		box := box.New(box.Config{
			Px: 8, Py: 1, TitlePos: "Top", ContentAlign: "Center"},
		)
		link :=
			box.String("Follow link to create a new org",
				"https://aex.dev.azure.com/signup\n\nRe-run azd pipeline config after creating the organization",
			)
		// the box library has an issue using strings with color format
		// that's why the color format is added after creating the box
		link = strings.Replace(link, "https://aex.dev.azure.com/signup", output.WithLinkFormat("https://aex.dev.azure.com/signup"), 1)
		link = strings.Replace(link, "azd pipeline config", output.WithHighLightFormat("azd pipeline config"), 1)
		console.Message(ctx, fmt.Sprintf("\n%s", link))
		os.Exit(0)
	}
	return orgName
}

// helper method to ensure an Azure DevOps organization name exists either in .env or system environment variables
func EnsureOrgNameExists(ctx context.Context, env *environment.Environment, console input.Console) (orgName string, err error) {
	orgName, err = ensureConfigExists(ctx, env, AzDoEnvironmentOrgName, "azure devops organization name")
	if err == nil {
		return orgName, err
	}

	// Org name not found.
	// Trying to get it using PAT
	pat := os.Getenv(AzDoPatName)
	organizationsList, err := getUserOrganizations(ctx, pat)
	if err == nil {
		// PAT work to fetch organizations
		orgName = pickOrganizationFromList(ctx, organizationsList, console)
	} else {
		// PAT can't read orgs. Use manual input
		orgName, err = console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Please enter an Azure DevOps Organization Name:",
			DefaultValue: "",
		})
		if err != nil {
			return "", fmt.Errorf("asking for new project name: %w", err)
		}
	}

	// ignoring if org can't be persisted to env
	_ = saveEnvironmentConfig(AzDoEnvironmentOrgName, orgName, env)

	return orgName, nil
}

// helper function to save configuration values to .env file
func saveEnvironmentConfig(key string, value string, env *environment.Environment) error {
	env.Values[key] = value
	err := env.Save()

	if err != nil {
		return err
	}
	return nil
}
