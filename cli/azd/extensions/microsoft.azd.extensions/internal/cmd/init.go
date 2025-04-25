// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/resources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new AZD extension project",
		RunE: func(cmd *cobra.Command, args []string) error {
			internal.WriteCommandHeader(
				"Initialize a new azd extension project (azd x init)",
				"Initializes a new azd extension project from a template",
			)

			fmt.Println()
			err := runInitAction(cmd.Context())
			if err != nil {
				return err
			}

			internal.WriteCommandSuccess("Extension initialized successfully!")
			return nil
		},
	}
}

func runInitAction(ctx context.Context) error {
	// Create a new context that includes the AZD access token
	ctx = azdext.WithAccessToken(ctx)

	// Create a new AZD client
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}

	defer azdClient.Close()

	extensionMetadata, err := collectExtensionMetadata(ctx, azdClient)
	if err != nil {
		return fmt.Errorf("failed to collect extension metadata: %w", err)
	}

	taskList := ux.NewTaskList(nil)
	taskList.AddTask(ux.TaskOptions{
		Title: fmt.Sprintf("Creating extension directory %s", extensionMetadata.Id),
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			if err := createExtensionDirectory(ctx, azdClient, extensionMetadata); err != nil {
				return ux.Error, common.NewDetailedError(
					"Error creating directory",
					fmt.Errorf("failed to create extension directory: %w", err),
				)
			}

			return ux.Success, nil
		},
	})

	taskList.AddTask(ux.TaskOptions{
		Title: "Create local azd extension source",
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			if has, err := hasLocalRegistry(); err == nil && has {
				return ux.Skipped, nil
			}

			if err := createLocalRegistry(ctx, azdClient); err != nil {
				return ux.Error, common.NewDetailedError(
					"Registry creation failed",
					fmt.Errorf("failed to create local registry: %w", err),
				)
			}

			return ux.Success, nil
		},
	})

	taskList.AddTask(ux.TaskOptions{
		Title: "Build extension",
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			cmd := exec.Command("azd", "x", "build", "--skip-install")
			cmd.Dir = extensionMetadata.Path

			if err := cmd.Run(); err != nil {
				return ux.Error, common.NewDetailedError(
					"Build failed",
					fmt.Errorf("failed to build extension: %w", err),
				)
			}

			return ux.Success, nil
		},
	})

	taskList.AddTask(ux.TaskOptions{
		Title: "Package extension",
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			cmd := exec.Command("azd", "x", "package")
			cmd.Dir = extensionMetadata.Path

			if err := cmd.Run(); err != nil {
				return ux.Error, common.NewDetailedError(
					"Package failed",
					fmt.Errorf("failed to package extension: %w", err),
				)
			}
			return ux.Success, nil
		},
	})

	taskList.AddTask(ux.TaskOptions{
		Title: "Install extension",
		Action: func(spf ux.SetProgressFunc) (ux.TaskState, error) {
			/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments */
			cmd := exec.Command("azd", "ext", "install", extensionMetadata.Id, "--source", "local")
			cmd.Dir = extensionMetadata.Path

			if err := cmd.Run(); err != nil {
				return ux.Error, common.NewDetailedError(
					"Install failed",
					fmt.Errorf("failed to install extension: %w", err),
				)
			}
			return ux.Success, nil
		},
	})

	if err := taskList.Run(); err != nil {
		return fmt.Errorf("failed running init tasks: %w", err)
	}

	fmt.Printf("Run %s to try your extension now.\n", output.WithHighLightFormat("azd %s -h", extensionMetadata.Namespace))
	fmt.Println()
	fmt.Printf("Run %s to rebuild the extension\n", output.WithHighLightFormat("azd x build"))
	fmt.Printf("Run %s to package the extension\n", output.WithHighLightFormat("azd x package"))
	fmt.Printf("Run %s to watch for changes and auto re-build the extension\n", output.WithHighLightFormat("azd x watch"))

	return nil
}

func collectExtensionMetadata(ctx context.Context, azdClient *azdext.AzdClient) (*models.ExtensionSchema, error) {
	idPrompt, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         "Enter a unique identifier for your extension",
			Placeholder:     "company.extension",
			RequiredMessage: "Extension ID is required",
			Required:        true,
			Hint: "Extension ID is used to identify your extension in the AZD ecosystem. " +
				"It should be unique and follow the format 'company.extension'.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for extension ID: %w", err)
	}

	displayNamePrompt, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         "Enter a display name for your extension",
			Placeholder:     "My Extension",
			RequiredMessage: "Display name is required",
			Required:        true,
			HelpMessage: "Display name is used to show the extension name in the AZD CLI. " +
				"It should be user-friendly and descriptive.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for display name: %w", err)
	}

	descriptionPrompt, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         "Enter a description for your extension",
			Placeholder:     "A brief description of your extension",
			RequiredMessage: "Description is required",
			Required:        true,
			HelpMessage: "Description is used to provide more information about your extension. " +
				"It should be concise and informative.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for description: %w", err)
	}

	tagsPrompt, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         "Enter tags for your extension (comma-separated)",
			Placeholder:     "tag1, tag2",
			RequiredMessage: "Tags are required",
			Required:        true,
			HelpMessage: "Tags are used to categorize your extension. " +
				"You can enter multiple tags separated by commas.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for tags: %w", err)
	}

	namespacePrompt, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         "Enter a namespace for your extension",
			RequiredMessage: "Namespace is required",
			Required:        true,
			HelpMessage: "Namespace is used to group custom commands into a single command " +
				"group used for executing the extension.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for namespace: %w", err)
	}

	capabilitiesPrompt, err := azdClient.Prompt().MultiSelect(ctx, &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: "Select capabilities for your extension",
			Choices: []*azdext.MultiSelectChoice{
				{
					Label: "Custom Commands",
					Value: "custom-commands",
				},
				{
					Label: "Lifecycle Events",
					Value: "lifecycle-events",
				},
			},
			EnableFiltering: internal.ToPtr(false),
			DisplayNumbers:  internal.ToPtr(false),
			HelpMessage: "Capabilities define the features and functionalities of your extension. " +
				"You can select multiple capabilities.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for capabilities: %w", err)
	}

	languageChoices := []*azdext.SelectChoice{
		{
			Label: "Go",
			Value: "go",
		},
		{
			Label: "C#",
			Value: "dotnet",
		},
		{
			Label: "Python",
			Value: "python",
		},
	}

	programmingLanguagePrompt, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         "Select a programming language for your extension",
			Choices:         languageChoices,
			EnableFiltering: internal.ToPtr(false),
			DisplayNumbers:  internal.ToPtr(false),
			HelpMessage: "Programming language is used to define the language in which your extension is written. " +
				"You can select one programming language.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for programming language: %w", err)
	}

	capabilities := make([]extensions.CapabilityType, len(capabilitiesPrompt.Values))
	for i, capability := range capabilitiesPrompt.Values {
		capabilities[i] = extensions.CapabilityType(capability.Value)
	}

	tags := []string{}
	strings.Split(tagsPrompt.Value, ",")
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			tags = append(tags, tag)
		}
	}

	absExtensionPath, err := filepath.Abs(idPrompt.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for extension directory: %w", err)
	}

	return &models.ExtensionSchema{
		Id:           idPrompt.Value,
		DisplayName:  displayNamePrompt.Value,
		Description:  descriptionPrompt.Value,
		Namespace:    namespacePrompt.Value,
		Capabilities: capabilities,
		Language:     languageChoices[*programmingLanguagePrompt.Value].Value,
		Tags:         tags,
		Usage:        fmt.Sprintf("azd %s <command> [options]", namespacePrompt.Value),
		Version:      "0.0.1",
		Path:         absExtensionPath,
	}, nil
}

func createExtensionDirectory(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	extensionMetadata *models.ExtensionSchema,
) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current working directory: %w", err)
	}

	extensionPath := filepath.Join(cwd, extensionMetadata.Id)

	info, err := os.Stat(extensionPath)
	if err == nil && info.IsDir() {
		azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message: fmt.Sprintf(
					"The extension directory '%s' already exists. Do you want to continue?",
					extensionMetadata.Id,
				),
				DefaultValue: internal.ToPtr(false),
			},
		})
	}

	if os.IsNotExist(err) {
		// Create the extension directory
		if err := os.MkdirAll(extensionPath, internal.PermissionDirectory); err != nil {
			return fmt.Errorf("failed to create extension directory: %w", err)
		}
	}

	// Create project from template.
	templateMetadata := &ExtensionTemplate{
		Metadata: extensionMetadata,
		DotNet: &DotNetTemplate{
			Namespace: internal.ToPascalCase(extensionMetadata.Id),
			ExeName:   strings.ReplaceAll(extensionMetadata.Id, ".", "-"),
		},
	}

	templatePath := path.Join("languages", extensionMetadata.Language)
	err = copyAndProcessTemplates(resources.Languages, templatePath, extensionPath, templateMetadata)
	if err != nil {
		return fmt.Errorf("failed to copy and process templates: %w", err)
	}

	// Create the extension.yaml file
	yamlBytes, err := yaml.Marshal(extensionMetadata)
	if err != nil {
		return fmt.Errorf("failed to marshal extension metadata to YAML: %w", err)
	}

	extensionFilePath := filepath.Join(extensionPath, "extension.yaml")
	if err := os.WriteFile(extensionFilePath, yamlBytes, internal.PermissionFile); err != nil {
		return fmt.Errorf("failed to create extension.yaml file: %w", err)
	}

	return nil
}

func copyAndProcessTemplates(srcFS fs.FS, srcDir, destDir string, data any) error {
	return fs.WalkDir(srcFS, srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to access path %s: %w", path, err)
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("failed to calculate relative path: %w", err)
		}

		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			if err := os.MkdirAll(destPath, internal.PermissionDirectory); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", destPath, err)
			}
			return nil
		}

		fileBytes, err := fs.ReadFile(srcFS, path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		if strings.HasSuffix(path, ".tmpl") {
			tmpl, err := template.New(filepath.Base(path)).Parse(string(fileBytes))
			if err != nil {
				return fmt.Errorf("failed to parse template %s: %w", path, err)
			}

			var processed bytes.Buffer
			if err := tmpl.Execute(&processed, data); err != nil {
				return fmt.Errorf("failed to execute template %s: %w", path, err)
			}

			destPath = strings.TrimSuffix(destPath, ".tmpl")
			fileBytes = processed.Bytes()
		}

		if err := os.WriteFile(destPath, fileBytes, internal.PermissionFile); err != nil {
			return fmt.Errorf("failed to write file %s: %w", destPath, err)
		}

		return nil
	})
}

func hasLocalRegistry() (bool, error) {
	cmdBytes, err := exec.Command("azd", "ext", "source", "list", "-o", "json").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("failed to execute command: %w", err)
	}

	var extensionSources []any
	if err := json.Unmarshal(cmdBytes, &extensionSources); err != nil {
		return false, fmt.Errorf("failed to unmarshal command output: %w", err)
	}

	for _, source := range extensionSources {
		extensionSource, ok := source.(map[string]any)
		if ok {
			if extensionSource["name"] == "local" && extensionSource["type"] == "file" {
				return true, nil
			}
		}
	}

	return false, nil
}

func createLocalRegistry(ctx context.Context, azdClient *azdext.AzdClient) error {
	azdConfigDir := os.Getenv("AZD_CONFIG_DIR")
	if azdConfigDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory: %w", err)
		}
		azdConfigDir = filepath.Join(homeDir, ".azd")
	}

	localRegistryPath := filepath.Join(azdConfigDir, "registry.json")
	emptyRegistry := map[string]any{
		"registry": []any{},
	}

	registryJson, err := json.MarshalIndent(emptyRegistry, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal empty registry: %w", err)
	}

	if err := os.WriteFile(localRegistryPath, registryJson, internal.PermissionFile); err != nil {
		return fmt.Errorf("failed to create local registry file: %w", err)
	}

	setupRegistryWorkflow := azdext.Workflow{
		Name: "setup-local-registry",
		Steps: []*azdext.WorkflowStep{
			{
				Command: &azdext.WorkflowCommand{
					Args: []string{
						"ext", "source", "add",
						"--name", "local",
						"--type", "file",
						"--location", "registry.json",
					},
				},
			},
		},
	}

	_, err = azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
		Workflow: &setupRegistryWorkflow,
	})
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}

	return nil
}

type ExtensionTemplate struct {
	Metadata *models.ExtensionSchema
	DotNet   *DotNetTemplate
}

type DotNetTemplate struct {
	Namespace string
	ExeName   string
}
