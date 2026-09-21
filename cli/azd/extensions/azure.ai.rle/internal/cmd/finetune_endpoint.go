// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func resolveFinetuneEndpoint(flagValue string, projectEndpoint string) (string, error) {
	raw := strings.TrimSpace(flagValue)
	if raw != "" {
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
		return "", invalidFinetuneEndpointError(fmt.Sprintf("invalid fine-tuning API endpoint: %v", err))
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", invalidFinetuneEndpointError("fine-tuning API endpoint must use https")
	}
	if u.Hostname() == "" {
		return "", invalidFinetuneEndpointError("fine-tuning API endpoint must include a host")
	}

	u.Scheme = "https"
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
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
