// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
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
	flags.projectResourceId, err = resolveVoiceProjectResourceID(
		ctx, azdClient, env.Name, flags.projectResourceId,
	)
	if err != nil {
		return err
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
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolving current directory: %w", err)
	}
	serviceDir, _, err := voiceServiceLayout(projectConfig.GetPath(), cwd, agentName)
	if err != nil {
		return err
	}
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
	projectRoot, err := filepath.Abs(projectConfig.GetPath())
	if err != nil {
		return fmt.Errorf("resolving project root: %w", err)
	}
	serviceRelPath, err := filepath.Rel(projectRoot, serviceDir)
	if err != nil {
		return fmt.Errorf("resolving voice agent path relative to project: %w", err)
	}
	return action.addVoiceAgentToProject(ctx, filepath.ToSlash(serviceRelPath), voiceDef)
}

func resolveVoiceProjectResourceID(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	explicitProjectID string,
) (string, error) {
	if strings.TrimSpace(explicitProjectID) != "" {
		return explicitProjectID, nil
	}
	configuredProjectID, err := getEnvValue(ctx, azdClient, envName, "AZURE_AI_PROJECT_ID")
	if err != nil {
		return "", fmt.Errorf("reading AZURE_AI_PROJECT_ID from environment %q: %w", envName, err)
	}
	if configuredProjectID == "" {
		return "", nil
	}
	if _, err := extractProjectDetails(configuredProjectID); err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidProjectResourceId,
			fmt.Sprintf("invalid AZURE_AI_PROJECT_ID in environment %q: %s", envName, err),
			"Set AZURE_AI_PROJECT_ID to a Foundry project resource ID in the format "+
				"/subscriptions/<SUBSCRIPTION_ID>/resourceGroups/<RESOURCE_GROUP>/providers/"+
				"Microsoft.CognitiveServices/accounts/<ACCOUNT_NAME>/projects/<PROJECT_NAME>.",
		)
	}
	return configuredProjectID, nil
}

func voiceServiceLayout(projectPath, cwd, agentName string) (string, string, error) {
	projectRoot, err := filepath.Abs(projectPath)
	if err != nil {
		return "", "", fmt.Errorf("resolving project root: %w", err)
	}
	relativeCwd, err := filepath.Rel(projectRoot, cwd)
	if err != nil || relativeCwd == ".." || strings.HasPrefix(relativeCwd, ".."+string(filepath.Separator)) {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"current directory is outside the resolved azd project",
			"run the command from the project directory or one of its subdirectories",
		)
	}
	serviceRelPath := filepath.Join(relativeCwd, "src", agentName)
	return filepath.Join(projectRoot, serviceRelPath), filepath.ToSlash(serviceRelPath), nil
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
