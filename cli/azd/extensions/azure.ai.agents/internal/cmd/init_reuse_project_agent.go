// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"go.yaml.in/yaml/v3"
)

// projectManifestCandidates lists the project manifest file names scanned when
// looking for an already-configured agent service, in priority order.
var projectManifestCandidates = []string{"azure.yaml", "azure.yml"}

// projectAgentService is an agent already declared in the project's azure.yaml.
// ServiceName is the key under services:; AgentName is the agent's own name from
// the inline definition, which may differ from the service key.
type projectAgentService struct {
	ServiceName string
	AgentName   string
}

// azureYamlAgentServices is the minimal view needed to spot agent services in a
// unified azure.yaml. yaml.v3 ignores every other key, so this stays tolerant of
// the rest of the (large) service schema.
type azureYamlAgentServices struct {
	Services map[string]struct {
		Host string `yaml:"host"`
		Name string `yaml:"name"`
		Kind string `yaml:"kind"`
		// Config carries the deprecated config-nested definition shape.
		Config struct {
			Name string `yaml:"name"`
			Kind string `yaml:"kind"`
		} `yaml:"config"`
	} `yaml:"services"`
}

// findProjectManifest returns the path to the project's azure.yaml in dir, or an
// empty string when none exists. The scan is shallow, mirroring
// findExistingAgentYaml.
func findProjectManifest(dir string) (string, error) {
	for _, name := range projectManifestCandidates {
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("checking for %s: %w", candidate, err)
		}
		if info.IsDir() {
			continue
		}
		return candidate, nil
	}

	return "", nil
}

// findProjectAgentServices returns the agent services already declared in the
// project manifest at path, sorted by service name so output is deterministic.
//
// The agent definition is carried inline on the service entry in the unified
// azure.yaml format; older projects nest it under config:. Both shapes are
// recognized, matching adoptedAgentNameConfig.
//
// A manifest that cannot be parsed yields no services rather than an error: the
// caller treats "nothing detected" as "fall through to the normal init prompts",
// which is the safe outcome for a malformed file.
func findProjectAgentServices(path string) ([]projectAgentService, error) {
	//nolint:gosec // path comes from findProjectManifest against a user-controlled directory
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var doc azureYamlAgentServices
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil
	}

	var found []projectAgentService
	for serviceName, svc := range doc.Services {
		if strings.TrimSpace(svc.Host) != AiAgentHost {
			continue
		}

		agentName := strings.TrimSpace(svc.Name)
		if strings.TrimSpace(svc.Kind) == "" && strings.TrimSpace(svc.Config.Kind) != "" {
			// Deprecated config-nested definition.
			agentName = strings.TrimSpace(svc.Config.Name)
		}
		if agentName == "" {
			agentName = serviceName
		}

		found = append(found, projectAgentService{ServiceName: serviceName, AgentName: agentName})
	}

	slices.SortFunc(found, func(a, b projectAgentService) int {
		return strings.Compare(a.ServiceName, b.ServiceName)
	})

	return found, nil
}

// describeProjectAgentServices renders the detected agent services for a prompt
// or status line, e.g. `"chat" (agent: my-chat-agent)`.
func describeProjectAgentServices(services []projectAgentService) string {
	parts := make([]string, 0, len(services))
	for _, svc := range services {
		if svc.AgentName != "" && svc.AgentName != svc.ServiceName {
			parts = append(parts, fmt.Sprintf("%q (agent: %s)", svc.ServiceName, svc.AgentName))
		} else {
			parts = append(parts, fmt.Sprintf("%q", svc.ServiceName))
		}
	}
	return strings.Join(parts, ", ")
}

// runReuseProjectAgentServices completes init for a project whose azure.yaml
// already declares its agents, without re-asking for the values the manifest
// already answers (agent name, protocols, deploy mode, ...).
//
// The definitions already live in azure.yaml, so there is nothing to write:
// this ensures an azd environment exists and then hands off to the shared
// next-step resolver. It mirrors runReuseDefinition (issue #7268), which does
// the same for a bare on-disk agent.yaml; the unified azure.yaml format moved
// the definition inline, and this is the inline equivalent.
func runReuseProjectAgentServices(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	srcDir string,
	manifestDisplayPath string,
	services []projectAgentService,
) error {
	fmt.Println(color.HiBlackString(
		"Detected existing agent configuration in %s: %s.",
		manifestDisplayPath,
		describeProjectAgentServices(services),
	))

	if _, err := ensureProject(ctx, flags, azdClient, "."); err != nil {
		return err
	}

	env := getExistingEnvironment(ctx, flags.env, azdClient)
	if env == nil {
		envName := flags.env
		if envName == "" {
			envName = sanitizeAgentName(services[0].AgentName + "-dev")
		}
		var err error
		env, err = createNewEnvironment(ctx, azdClient, envName)
		if err != nil {
			return fmt.Errorf("failed to create azd environment: %w", err)
		}
		flags.env = env.Name
	}

	fmt.Println(color.HiBlackString(
		"Reusing the agent configuration already in %s.", manifestDisplayPath,
	))

	// Advisory only, matching the other reuse paths. The deploy-mode specific
	// checks need a CodeConfiguration, which is not re-parsed here.
	validatePostInit(srcDir, nil)

	state, _ := nextstep.AssembleState(ctx, azdClient)
	_ = printAllNextIfTerminal(os.Stdout, nextstep.ResolveAfterInit(state, readmeExistsForProject(ctx, azdClient)))

	return nil
}
