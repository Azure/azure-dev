// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

func runInitVoice(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	projectTargetDir string,
	createdFolderDisplay string,
) error {
	projectConfig, err := ensureProject(ctx, flags, azdClient, projectTargetDir)
	if err != nil {
		return err
	}

	env := getExistingEnvironment(ctx, flags.env, azdClient)
	if env == nil {
		env, err = createNewEnvironment(ctx, azdClient, deriveEnvName(flags, projectTargetDir))
		if err != nil {
			return err
		}
	}

	azureContext, err := loadAzureContext(ctx, azdClient, env.Name)
	if err != nil {
		return err
	}
	result, err := configureFoundryProject(
		ctx,
		azdClient,
		azureContext,
		env.Name,
		flags.projectResourceId,
		flags.acrConnection,
		flags.noPrompt,
		true,
		false,
		false,
	)
	if err != nil {
		return err
	}
	if err := setACREnvVar(ctx, azdClient, env.Name, true); err != nil {
		return err
	}

	agentName := strings.TrimSpace(flags.agentName)
	if result.FoundryProject != nil {
		agentName, err = resolveExistingAgentNameConflict(
			ctx,
			azdClient,
			env,
			result.Credential,
			flags.noPrompt,
			agentName,
		)
		if err != nil {
			return err
		}
	}

	serviceName := strings.ReplaceAll(agentName, " ", "")
	serviceDir := filepath.Join("src", agentName)
	action := &InitAction{
		azdClient:              azdClient,
		credential:             result.Credential,
		projectConfig:          projectConfig,
		environment:            env,
		flags:                  flags,
		createdFolderDisplay:   createdFolderDisplay,
		selectedFoundryProject: result.FoundryProject,
	}
	serviceDir, serviceName, err = action.resolveCollisions(
		ctx,
		agentName,
		serviceDir,
		serviceName,
	)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(serviceDir, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating voice agent service directory %q: %w", serviceDir, err)
	}

	voiceDef := voiceDefinitionForInit(flags, agentName)

	action.serviceNameOverride = serviceName
	return action.addVoiceAgentToProject(ctx, filepath.ToSlash(serviceDir), voiceDef)
}

func voiceDefinitionForInit(flags *initFlags, agentName string) *agent_yaml.VoiceAgent {
	model := strings.TrimSpace(flags.model)
	if model == "" {
		model = defaultVoiceModel
	}
	description := "Declarative (managed) voice speech-to-speech agent"
	voiceDef := &agent_yaml.VoiceAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind:        agent_yaml.AgentKindPromptVoice,
			Name:        agentName,
			Description: &description,
		},
		ModelType: agent_yaml.VoiceModelTypeManaged,
		Model:     &agent_yaml.Model{Id: model},
	}
	if voice := strings.TrimSpace(flags.voice); voice != "" {
		voiceDef.Voice = &voice
	}
	return voiceDef
}
