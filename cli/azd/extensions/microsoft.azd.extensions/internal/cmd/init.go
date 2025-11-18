// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
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
	"go.yaml.in/yaml/v3"
)

type initFlags struct {
	createRegistry bool
	noPrompt       bool
	id             string
	name           string
	capabilities   []string
	language       string
	namespace      string
}

func newInitCommand() *cobra.Command {
	flags := &initFlags{}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new AZD extension project",
		RunE: func(cmd *cobra.Command, args []string) error {
			internal.WriteCommandHeader(
				"Initialize a new azd extension project (azd x init)",
				"Initializes a new azd extension project from a template",
			)

			// Validate required parameters when in headless mode
			if flags.noPrompt {
				var missingParams []string
				if flags.id == "" {
					missingParams = append(missingParams, "--id")
				}
				if flags.name == "" {
					missingParams = append(missingParams, "--name")
				}
				if len(flags.capabilities) == 0 {
					missingParams = append(missingParams, "--capabilities")
				}
				if flags.language == "" {
					missingParams = append(missingParams, "--language")
				}

				if len(missingParams) > 0 {
					return fmt.Errorf(
						"when using --no-prompt, the following parameters are required: %s",
						strings.Join(missingParams, ", "),
					)
				}
			}

			err := runInitAction(cmd.Context(), flags)
			if err != nil {
				return err
			}

			internal.WriteCommandSuccess("Extension initialized successfully!")
			return nil
		},
	}

	initCmd.Flags().BoolVarP(
		&flags.createRegistry,
		"registry", "r", false,
		"When set will create a local extension source registry.",
	)

	initCmd.Flags().BoolVar(
		&flags.noPrompt,
		"no-prompt", false,
		"Skip all prompts by providing all required parameters via command-line flags.",
	)

	initCmd.Flags().StringVar(
		&flags.id,
		"id", "",
		"The extension identifier (e.g., company.extension).",
	)

	initCmd.Flags().StringVar(
		&flags.name,
		"name", "",
		"The display name for the extension.",
	)

	initCmd.Flags().StringSliceVar(
		&flags.capabilities,
		"capabilities", []string{},
		"The list of capabilities for the extension "+
			"(e.g., custom-commands,lifecycle-events,mcp-server,service-target-provider).",
	)

	initCmd.Flags().StringVar(
		&flags.language,
		"language", "",
		"The programming language for the extension (go, dotnet, javascript, python).",
	)

	initCmd.Flags().StringVar(
		&flags.namespace,
		"namespace", "",
		"The namespace for the extension commands.",
	)

	return initCmd
}

func runInitAction(ctx context.Context, flags *initFlags) error {
	// Create a new context that includes the AZD access token
	ctx = azdext.WithAccessToken(ctx)

	// Create a new AZD client
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}

	defer azdClient.Close()

	var extensionMetadata *models.ExtensionSchema
	if flags.noPrompt {
		// In headless mode, use the provided command-line arguments
		extensionMetadata, err = collectExtensionMetadataFromFlags(flags)
		if err != nil {
			return err
		}
	} else if !flags.createRegistry {
		// Interactive mode - collect metadata through prompts
		extensionMetadata, err = collectExtensionMetadata(ctx, azdClient)
		if err != nil {
			return fmt.Errorf("failed to collect extension metadata: %w", err)
		}

		fmt.Println()
		confirmResponse, err := azdClient.
			Prompt().
			Confirm(ctx, &azdext.ConfirmRequest{
				Options: &azdext.ConfirmOptions{
					Message:      fmt.Sprintf("Continue creating the extension at %s?", extensionMetadata.Id),
					DefaultValue: internal.ToPtr(false),
					Placeholder:  "no",
					HelpMessage:  "Confirm if you want to continue creating the extension.",
				},
			})
		if err != nil {
			return fmt.Errorf("failed to confirm extension, %w", err)
		}

		if !*confirmResponse.Value {
			return errors.New("extension creation cancelled by user")
		}
	}

	localRegistryExists := false

	createLocalExtensionSourceAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
		if has, err := internal.HasLocalRegistry(); err == nil && has {
			localRegistryExists = true
			return ux.Skipped, nil
		}

		if err := internal.CreateLocalRegistry(); err != nil {
			return ux.Error, common.NewDetailedError(
				"Registry creation failed",
				fmt.Errorf("failed to create local registry: %w", err),
			)
		}

		return ux.Success, nil
	}

	createExtensionDirectoryAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
		if err := createExtensionDirectory(ctx, azdClient, extensionMetadata); err != nil {
			return ux.Error, common.NewDetailedError(
				"Error creating directory",
				fmt.Errorf("failed to create extension directory: %w", err),
			)
		}

		return ux.Success, nil
	}

	buildExtensionAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
		cmd := exec.Command("azd", "x", "build", "--skip-install")
		cmd.Dir = extensionMetadata.Path

		if err := cmd.Run(); err != nil {
			return ux.Error, common.NewDetailedError(
				"Build failed",
				fmt.Errorf("failed to build extension: %w", err),
			)
		}

		return ux.Success, nil
	}

	packageExtensionAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
		cmd := exec.Command("azd", "x", "pack")
		cmd.Dir = extensionMetadata.Path

		if err := cmd.Run(); err != nil {
			return ux.Error, common.NewDetailedError(
				"Package failed",
				fmt.Errorf("failed to package extension: %w", err),
			)
		}
		return ux.Success, nil
	}

	publishExtensionAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
		cmd := exec.Command("azd", "x", "publish")
		cmd.Dir = extensionMetadata.Path

		if err := cmd.Run(); err != nil {
			return ux.Error, common.NewDetailedError(
				"Publish failed",
				fmt.Errorf("failed to package extension: %w", err),
			)
		}
		return ux.Success, nil
	}

	installExtensionAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
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
	}

	taskList := ux.NewTaskList(nil)

	if flags.createRegistry {
		taskList.AddTask(ux.TaskOptions{
			Title:  "Create local azd extension source",
			Action: createLocalExtensionSourceAction,
		})
	} else {
		taskList.
			AddTask(ux.TaskOptions{
				Title:  "Create local azd extension source",
				Action: createLocalExtensionSourceAction,
			}).
			AddTask(ux.TaskOptions{
				Title:  fmt.Sprintf("Creating extension directory %s", output.WithHighLightFormat(extensionMetadata.Id)),
				Action: createExtensionDirectoryAction,
			}).
			AddTask(ux.TaskOptions{
				Title:  "Build extension",
				Action: buildExtensionAction,
			}).
			AddTask(ux.TaskOptions{
				Title:  "Package extension",
				Action: packageExtensionAction,
			}).
			AddTask(ux.TaskOptions{
				Title:  "Publish extension to local extension source",
				Action: publishExtensionAction,
			}).
			AddTask(ux.TaskOptions{
				Title:  "Install extension",
				Action: installExtensionAction,
			})
	}

	if err := taskList.Run(); err != nil {
		return fmt.Errorf("failed running init tasks: %w", err)
	}

	if localRegistryExists {
		fmt.Println(output.WithWarningFormat("Local extension source already exists."))
		fmt.Println()
	}

	if !flags.createRegistry {
		fmt.Println(output.WithBold("Try out the extension"))
		fmt.Printf(
			"- Run %s to try your extension now.\n",
			output.WithHighLightFormat("azd %s -h", extensionMetadata.Namespace),
		)
		fmt.Println()
		fmt.Println(output.WithBold("Next Steps"))
		fmt.Printf(
			"- Navigate to the %s directory and start building your extension.\n",
			output.WithHighLightFormat(extensionMetadata.Id),
		)
		fmt.Println()
		fmt.Println(output.WithBold("Iterate on the extension"))
		fmt.Printf(
			"- Run %s to watch for code changes and auto re-build the extension\n",
			output.WithHighLightFormat("azd x watch"),
		)
		fmt.Printf("- Run %s to rebuild the extension\n", output.WithHighLightFormat("azd x build"))
		fmt.Println()
		fmt.Println(output.WithBold("Package, release and publish the extension"))
		fmt.Printf("- Run %s to package the extension\n", output.WithHighLightFormat("azd x pack"))
		fmt.Printf("- Run %s to create a GitHub release for your extension\n", output.WithHighLightFormat("azd x release"))
		fmt.Printf("- Run %s to publish the extension\n", output.WithHighLightFormat("azd x publish"))
		fmt.Println()
	}

	return nil
}

// collectExtensionMetadataFromFlags creates extension metadata from command-line flags
func collectExtensionMetadataFromFlags(flags *initFlags) (*models.ExtensionSchema, error) {
	// Validate that the language is supported
	validLanguages := map[string]bool{
		"go":         true,
		"dotnet":     true,
		"javascript": true,
		"python":     true,
	}

	if !validLanguages[flags.language] {
		return nil, fmt.Errorf(
			"invalid language '%s', supported languages are: go, dotnet, javascript, python",
			flags.language,
		)
	}

	// Validate capabilities
	validCapabilities := map[string]bool{
		"custom-commands":         true,
		"lifecycle-events":        true,
		"mcp-server":              true,
		"service-target-provider": true,
	}

	for _, cap := range flags.capabilities {
		if !validCapabilities[cap] {
			return nil, fmt.Errorf(
				"invalid capability '%s', supported capabilities are: "+
					"custom-commands, lifecycle-events, mcp-server, service-target-provider",
				cap,
			)
		}
	}

	// Convert capabilities from string slice to CapabilityType slice
	capabilities := make([]extensions.CapabilityType, len(flags.capabilities))
	for i, cap := range flags.capabilities {
		capabilities[i] = extensions.CapabilityType(cap)
	}

	// Use default empty tags
	tags := []string{}

	// Set a default description
	description := "An AZD extension"

	// Default namespace to ID if not provided
	namespace := flags.id
	if flags.namespace != "" {
		namespace = flags.namespace
	}

	absExtensionPath, err := filepath.Abs(flags.id)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for extension directory: %w", err)
	}

	return &models.ExtensionSchema{
		Id:           flags.id,
		DisplayName:  flags.name,
		Description:  description,
		Namespace:    namespace,
		Capabilities: capabilities,
		Language:     flags.language,
		Tags:         tags,
		Usage:        fmt.Sprintf("azd %s <command> [options]", namespace),
		Version:      "0.0.1",
		Path:         absExtensionPath,
	}, nil
}

func collectExtensionMetadata(ctx context.Context, azdClient *azdext.AzdClient) (*models.ExtensionSchema, error) {
	fmt.Println()
	fmt.Println("Please provide the following information to create your extension.")
	fmt.Printf("Values can be changed later in the %s file.\n", output.WithHighLightFormat("extension.yaml"))
	fmt.Println()

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
				{
					Label: "MCP Server",
					Value: "mcp-server",
				},
				{
					Label: "Service Target Provider",
					Value: "service-target-provider",
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
			Label: "JavaScript",
			Value: "javascript",
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
			ExeName:   extensionMetadata.SafeDashId(),
		},
	}

	templatePath := path.Join("languages", extensionMetadata.Language)
	err = copyAndProcessTemplates(resources.Languages, templatePath, extensionPath, templateMetadata)
	if err != nil {
		return fmt.Errorf("failed to copy and process templates: %w", err)
	}

	if extensionMetadata.Language != "go" {
		protoSrcPath := path.Join("languages", "proto")
		protoDstPath := filepath.Join(extensionPath, "proto")

		err = copyAndProcessTemplates(resources.Languages, protoSrcPath, protoDstPath, templateMetadata)
		if err != nil {
			return fmt.Errorf("failed to copy and process proto templates: %w", err)
		}
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

type ExtensionTemplate struct {
	Metadata *models.ExtensionSchema
	DotNet   *DotNetTemplate
}

type DotNetTemplate struct {
	Namespace string
	ExeName   string
}
