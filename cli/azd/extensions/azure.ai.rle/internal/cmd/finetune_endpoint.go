// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// rleTrainEndpointEnvVar overrides the Foundry-derived fine-tuning endpoint.
// Set it when an environment routes RLE jobs to a facade or mock service.
const rleTrainEndpointEnvVar = "RLE_TRAIN_ENDPOINT"

// RLE_ENV_OVERRIDE is a development aid. When set, train sends its value in the
// rleEnvOverride header so the service runs the job against that RLE environment.
const (
	rleEnvOverrideEnvVar = "RLE_ENV_OVERRIDE"
	rleEnvOverrideHeader = "rleEnvOverride"
)

// resolveFinetuneEndpoint picks the fine-tuning API the RLE commands talk to:
// the --endpoint flag, then RLE_TRAIN_ENDPOINT, else the endpoint derived from
// the Foundry project. Train, jobs and cancel share it; monitoring uses the
// Foundry project URL directly for fine-tuning status and RLE persistence reads.
func resolveFinetuneEndpoint(flagValue string, projectEndpoint string) (string, error) {
	if raw := strings.TrimSpace(flagValue); raw != "" {
		return normalizeFinetuneEndpoint(raw)
	}
	if raw := strings.TrimSpace(os.Getenv(rleTrainEndpointEnvVar)); raw != "" {
		return normalizeFinetuneEndpoint(raw)
	}
	return finetuneEndpointFromFoundryProject(projectEndpoint)
}

func finetuneEndpointFromFoundryProject(projectEndpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(projectEndpoint))
	if err != nil {
		return "", invalidProjectEndpointError(fmt.Sprintf("invalid Foundry project endpoint: %v", err))
	}

	const foundryHostSuffix = ".services.ai.azure.com"
	host := strings.ToLower(u.Hostname())
	account := strings.TrimSuffix(host, foundryHostSuffix)
	if account == "" || account == host {
		return "", invalidProjectEndpointError(
			"Foundry project endpoint host must end with .services.ai.azure.com",
		)
	}

	return (&url.URL{
		Scheme: "https",
		Host:   account + ".openai.azure.com",
	}).String(), nil
}

func normalizeFinetuneEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", invalidFinetuneEndpointError("invalid fine-tuning API endpoint URL")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", invalidFinetuneEndpointError("fine-tuning API endpoint must use https")
	}
	if u.Hostname() == "" {
		return "", invalidFinetuneEndpointError("fine-tuning API endpoint must include a host")
	}

	u.Scheme = "https"
	u.User = nil
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String(), nil
}

func invalidFinetuneEndpointError(message string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       "rle_invalid_train_endpoint",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Pass --endpoint https://<resource>.openai.azure.com.",
	}
}
