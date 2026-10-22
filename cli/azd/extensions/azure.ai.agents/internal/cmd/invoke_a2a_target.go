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

// validateA2ATarget checks implementation capabilities from the selected local service.
func (a *InvokeAction) validateA2ATarget(ctx context.Context, rc *remoteContext) error {
	if a.endpoint != nil || rc.azdClient == nil || rc.serviceName == "" {
		return nil
	}
	// Current source cannot describe an explicitly selected historical deployment.
	if rc.version != "" && rc.version != rc.deployedVersion {
		return nil
	}
	svc, proj, err := resolveAgentService(ctx, rc.azdClient, rc.serviceName, a.noPrompt)
	if err != nil {
		return fmt.Errorf("resolve A2A target service: %w", err)
	}
	hosted, isHosted, found, _, err := projectpkg.AgentDefinitionFromResolvedService(svc, proj.Path)
	if err != nil {
		// Let Foundry enforce capabilities when the local definition is unavailable.
		log.Printf("could not inspect local A2A capabilities: %v", err)
		return nil
	}
	if !found || !isHosted {
		return nil
	}
	return validateHostedA2ACompatibility(hosted.Protocols, a.a2aProtocolVersion())
}

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
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"the hosted agent does not declare an A2A implementation",
			"implement A2A 1.0 and declare it in the selected service's protocols",
		)
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
