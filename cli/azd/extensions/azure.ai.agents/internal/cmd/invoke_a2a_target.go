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

// resolveA2ATarget uses the selected service's implementation protocols, not its
// public endpoint protocols. Foundry remains responsible for dispatch and feature
// gating. Explicit endpoints have no local target definition to infer from.
func (a *InvokeAction) resolveA2ATarget(ctx context.Context, rc *remoteContext) error {
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
		// Explicit remote invocation must not require a valid local build definition.
		// If it cannot be inspected, leave capability enforcement to Foundry.
		log.Printf("could not inspect local A2A capabilities: %v", err)
		return nil
	}
	if !found || !isHosted {
		return nil
	}
	version, err := selectA2ATargetVersion(hosted.Protocols, a.flags.a2aVersion)
	if err != nil {
		return err
	}
	a.resolvedA2AVersion = version
	return nil
}

// selectA2ATargetVersion validates native capabilities and selects a client wire
// dialect. A Responses implementation permits translation; merely exposing A2A
// on the public endpoint does not declare a native implementation.
func selectA2ATargetVersion(protocols []agent_yaml.ProtocolVersionRecord, explicit string) (string, error) {
	if explicit != "" {
		if err := validateA2AVersion(explicit); err != nil {
			return "", err
		}
	}
	var native string
	var nativeError error
	var responses bool
	for _, record := range protocols {
		switch strings.TrimSpace(record.Protocol) {
		case "responses":
			responses = true
		case "a2a":
			version, err := nativeA2AWireVersion(record.Version)
			if err != nil {
				nativeError = err
				continue
			}
			if native != "" && native != version {
				return "", exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					"the agent declares conflicting native A2A versions",
					"declare one supported native A2A version in the selected service's protocols",
				)
			}
			native = version
		}
	}

	// An unknown native dialect cannot select passthrough, but it does not
	// invalidate a separately implemented Responses translation interface.
	if nativeError != nil && !responses {
		return "", nativeError
	}
	if native == "0.3" && !responses {
		return "", exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"native A2A 0.3 passthrough is not supported by Foundry",
			"upgrade the application and its native A2A declaration to 1.0.0; "+
				"translation requires an implemented Responses protocol",
		)
	}
	if native == "1.0" && explicit == "0.3" && !responses {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"A2A 0.3 cannot invoke this native A2A 1.0-only agent",
			"use --a2a-version 1.0 and a matching request body; native A2A passthrough does not translate dialects",
		)
	}
	if explicit != "" {
		return explicit, nil
	}
	// Native 0.3 with Responses uses 1.0 translation, just like a target with
	// no native A2A declaration. File input still requires an explicit flag.
	return "1.0", nil
}

// nativeA2AWireVersion normalizes declared major.minor[.patch] versions to the
// major.minor wire selector. Future minor/major dialects are not assumed compatible.
func nativeA2AWireVersion(declared string) (string, error) {
	parts := strings.Split(declared, ".")
	valid := len(parts) == 2 || len(parts) == 3
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || strconv.Itoa(n) != part {
			valid = false
		}
	}
	if valid {
		version := strings.Join(parts[:2], ".")
		if version == "1.0" || version == "0.3" {
			return version, nil
		}
	}
	return "", exterrors.Validation(
		exterrors.CodeInvalidAgentManifest,
		fmt.Sprintf("unsupported native A2A version %q in the agent definition", declared),
		"declare a supported A2A version; native passthrough currently requires A2A 1.0.x",
	)
}
