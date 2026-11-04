// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	projectpkg "azureaiagent/internal/project"
)

// validateA2ATarget inspects local capabilities for advisory diagnostics only.
// Working-tree definitions do not establish deployed capabilities, even for the current version.
func (a *InvokeAction) validateA2ATarget(ctx context.Context, rc *remoteContext, version string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// An explicit session's bound deployment is not known from the current local definition.
	if a.endpoint != nil || a.flags.session != "" || rc.azdClient == nil || rc.serviceName == "" {
		return nil
	}
	// Current source cannot describe an explicitly selected historical deployment.
	if rc.version != "" && rc.version != rc.deployedVersion {
		return nil
	}
	svc, proj, err := resolveAgentService(ctx, rc.azdClient, rc.serviceName, a.noPrompt)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("could not resolve local A2A target service: %v", err)
		return nil
	}
	hosted, isHosted, found, _, err := projectpkg.AgentDefinitionFromResolvedService(svc, proj.Path)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		// Let Foundry enforce capabilities even when a readable local definition is invalid.
		log.Printf("could not inspect local A2A capabilities: %v", err)
		return nil
	}
	if !found || !isHosted {
		return nil
	}
	if err := validateHostedA2ACompatibility(hosted.Protocols, version); err != nil {
		log.Printf("local A2A compatibility finding (advisory; Foundry validates the deployed target): %v", err)
	}
	return nil
}

// validateHostedA2ACompatibility validates a declared A2A dialect against hosted support and the request.
func validateHostedA2ACompatibility(protocols []agent_yaml.ProtocolVersionRecord, requested string) error {
	var declared string
	for _, record := range protocols {
		if strings.TrimSpace(record.Protocol) != "a2a" {
			continue
		}
		version, err := normalizeA2AWireVersion(record.Version)
		if err != nil {
			return err
		}
		if declared != "" && declared != version {
			return exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				"the A2A hosted agent declares conflicting A2A versions",
				"declare one A2A major.minor version in the selected service's protocols",
			)
		}
		declared = version
	}
	if declared == "" {
		// Foundry owns routing when no A2A implementation is declared.
		return nil
	}
	if declared != "1.0" {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("A2A hosted-agent version %s is not supported by Foundry", declared),
			"use an A2A 1.0 hosted agent",
		)
	}
	if requested != declared {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("requested A2A version %s does not match the hosted-agent declaration %s", requested, declared),
			"use --a2a-version 1.0 and a matching request body; A2A hosted agents do not translate dialects",
		)
	}
	return nil
}

// normalizeA2AWireVersion checks declaration syntax, not supported dialects or deployment versions.
func normalizeA2AWireVersion(declared string) (string, error) {
	parts := strings.Split(declared, ".")
	valid := len(parts) == 2 || len(parts) == 3
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || strconv.Itoa(n) != part {
			valid = false
		}
	}
	if valid {
		return strings.Join(parts[:2], "."), nil
	}
	return "", exterrors.Validation(
		exterrors.CodeInvalidAgentManifest,
		fmt.Sprintf("malformed A2A version %q in the hosted-agent definition", declared),
		"use a major.minor or major.minor.patch A2A declaration",
	)
}
