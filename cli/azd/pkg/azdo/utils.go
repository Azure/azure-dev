// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
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
	patHeader := azuredevops.CreateBasicAuthHeaderValue("", pat)
	req, err := http.NewRequest(
		http.MethodGet,
		"https://app.vssps.visualstudio.com/_apis/profile/profiles/me?api-version=5.1",
		nil)
	if err != nil {
		return id, err
	}
	// auth
	req.Header.Add("Authorization", patHeader)
	req.Header.Add("Accept", "application/json;api-version=5.1")

	response, err := http.DefaultClient.Do(req)
	if response != nil && response.Body != nil {
		var err error
		defer func() {
			if closeError := response.Body.Close(); closeError != nil {
				err = closeError
			}
		}()
		body, err := ioutil.ReadAll(response.Body)
		if err != nil {
			return id, err
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
		body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
		err = json.Unmarshal(body, &responseValue)
		id = responseValue.Id
	}
	return id, nil
}

func getUserOrganizations(ctx context.Context, userId, pat string) (organizations []string, err error) {
	patHeader := azuredevops.CreateBasicAuthHeaderValue("", pat)
	req, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf("https://app.vssps.visualstudio.com/_apis/accounts?memberId=%s&api-version=5.1", userId),
		nil)
	if err != nil {
		return organizations, err
	}
	// auth
	req.Header.Add("Authorization", patHeader)
	req.Header.Add("Accept", "application/json;api-version=5.1")

	response, err := http.DefaultClient.Do(req)
	if response != nil && response.Body != nil {
		var err error
		defer func() {
			if closeError := response.Body.Close(); closeError != nil {
				err = closeError
			}
		}()
		body, err := ioutil.ReadAll(response.Body)
		if err != nil {
			return organizations, err
		}
		type azdoOrganization struct {
			Name string `json:"accountName"`
		}
		type azdoOrganizationResponse struct {
			Total         int                `json:"count"`
			Organizations []azdoOrganization `json:"value"`
		}
		var responseValue azdoOrganizationResponse
		body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
		err = json.Unmarshal(body, &responseValue)

		for _, orgName := range responseValue.Organizations {
			organizations = append(organizations, orgName.Name)
		}
	}
	return organizations, nil
}

// helper method to ensure an Azure DevOps organization name exists either in .env or system environment variables
func EnsureOrgNameExists(ctx context.Context, env *environment.Environment, console input.Console) (string, error) {
	value, err := ensureConfigExists(ctx, env, AzDoEnvironmentOrgName, "azure devops organization name")

	if err != nil {
		pat := os.Getenv(AzDoPatName)
		userId, _ := getUserProfileId(ctx, pat)
		organizationsList, _ := getUserOrganizations(ctx, userId, pat)
		organizationsList = append(organizationsList, "Create a new Organization")
		azdoOrgIndex, _ := console.Select(ctx, input.ConsoleOptions{
			Message:      "Select the Azure DevOps organization",
			Options:      organizationsList,
			DefaultValue: organizationsList[0],
		})
		azdoOrg := organizationsList[azdoOrgIndex]

		if azdoOrgIndex == (len(organizationsList) - 1) {
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
		value = azdoOrg

		// orgName, err := console.Prompt(ctx, input.ConsoleOptions{
		// 	Message:      "Please enter an Azure DevOps Organization Name:",
		// 	DefaultValue: "",
		// })
		// if err != nil {
		// 	return "", fmt.Errorf("asking for new project name: %w", err)
		// }

		_ = saveEnvironmentConfig(AzDoEnvironmentOrgName, value, env)

		// value = orgName
	}
	return value, nil
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
