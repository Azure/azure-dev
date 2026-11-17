// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"
	"strings"

	"azureaiagent/internal/pkg/paths"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// maxListedSensitivePaths caps how many re-included paths the warning names.
const maxListedSensitivePaths = 5

// CheckCodePackageSecrets returns a non-blocking warning when a hosted code agent's
// .agentignore re-includes paths that commonly hold secrets (.env files, .azure/, .git/),
// so deploying would upload them to the Foundry project. A check that can't complete is
// skipped; packaging reports any real problem with the service or its .agentignore.
func CheckCodePackageSecrets(
	ctx context.Context, svc *azdext.ServiceConfig, projectRoot string,
) []azdext.BetaServiceEventMessage {
	sensitive, err := serviceCodePackageSensitivePaths(ctx, svc, projectRoot)
	if err != nil {
		log.Printf("skipping %s secrets check for service %q: %v", agentIgnoreFileName, svc.GetName(), err)
		return nil
	}
	if len(sensitive) == 0 {
		return nil
	}

	listed := strings.Join(sensitive[:min(len(sensitive), maxListedSensitivePaths)], ", ")
	if extra := len(sensitive) - maxListedSensitivePaths; extra > 0 {
		listed += fmt.Sprintf(", and %d more", extra)
	}

	return []azdext.BetaServiceEventMessage{{
		Kind: azdext.BetaServiceEventMessageWarning,
		Message: fmt.Sprintf(
			"Deploying service %q will upload files that commonly hold secrets because %s re-includes them: %s",
			svc.GetName(), agentIgnoreFileName, listed,
		),
		Suggestion: fmt.Sprintf(
			"Remove the matching ! patterns from %s unless the agent must deploy these files.",
			agentIgnoreFileName,
		),
	}}
}

// serviceCodePackageSensitivePaths returns codePackageSensitivePaths for the service's
// source directory, or nil when the service doesn't package that directory.
func serviceCodePackageSensitivePaths(
	ctx context.Context, svc *azdext.ServiceConfig, projectRoot string,
) ([]string, error) {
	agentDef, isHosted, _, err := LoadHostedAgentDefinition(svc, projectRoot)
	if err != nil || !isHosted || agentDef.CodeConfiguration == nil || isDotnetBundled(agentDef.CodeConfiguration) {
		return nil, err
	}

	srcDir, err := paths.JoinAllowRoot(projectRoot, svc.GetRelativePath())
	if err != nil {
		return nil, err
	}

	return codePackageSensitivePaths(ctx, srcDir)
}
