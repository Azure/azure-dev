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

// finetuneEndpointEnvVar points at the Azure OpenAI resource that hosts the fine-tuning
// API (POST /openai/v1/fine_tuning/jobs), e.g. https://<resource>.openai.azure.com. This
// is a different resource than the Foundry project targeted by FOUNDRY_PROJECT_ENDPOINT.
const finetuneEndpointEnvVar = "AZD_AI_RLE_TRAIN_ENDPOINT"

func resolveFinetuneEndpoint(flagValue string) (string, error) {
	raw := strings.TrimSpace(flagValue)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(finetuneEndpointEnvVar))
	}
	if raw == "" {
		return "", &azdext.LocalError{
			Message:  "A fine-tuning API endpoint is required for train.",
			Code:     "rle_train_endpoint_required",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf(
				"Set %s=https://<resource>.openai.azure.com, or pass --endpoint.",
				finetuneEndpointEnvVar,
			),
		}
	}
	return normalizeFinetuneEndpoint(raw)
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
		Message:  message,
		Code:     "rle_invalid_train_endpoint",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf(
			"Set %s=https://<resource>.openai.azure.com.",
			finetuneEndpointEnvVar,
		),
	}
}
