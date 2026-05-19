// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"unicode"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/resources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/common"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
)

type initFlags struct {
	createRegistry bool
	noPrompt       bool
	id             string
	name           string
	capabilities   []string
	language       string
	namespace      string
	tags           []string
}

// extensionSchemaHeader is prepended to generated extension.yaml files so editor
// tooling (VS Code YAML extension) can resolve and validate against the schema.
const extensionSchemaHeader = "# yaml-language-server: $schema=" +
	"https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/cli/azd/extensions/extension.schema.json\n"

const (
	maxExtensionTags      = 10
	maxExtensionTagLength = 64
)

var extensionNamespacePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)*$`)

func newInitCommand(noPrompt *bool) *cobra.Command {
	flags := &initFlags{}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new azd extension project",
		Long: "Initializes a new azd extension project from a template.\n\n" +
			"When creating an extension project, the build step invokes the azd binary found on PATH. " +
			"Validation warning behavior during that nested build depends on the installed azd version.",
		RunE: func(cmd *cobra.Command, args []string) error {
			internal.WriteCommandHeader(
				"Initialize a new azd extension project (azd x init)",
				"Initializes a new azd extension project from a template",
			)

			if noPrompt != nil {
				flags.noPrompt = *noPrompt
			}

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
		fmt.Sprintf(
			"The list of capabilities for the extension (e.g., %s).",
			strings.Join(validCapabilityNames(), ","),
		),
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

	initCmd.Flags().StringSliceVar(
		&flags.tags,
		"tags", []string{},
		fmt.Sprintf(
			"Optional tags for the extension, comma-separated or repeatable (max %d tags, %d characters each).",
			maxExtensionTags,
			maxExtensionTagLength,
		),
	)

	return initCmd
}

func runInitAction(ctx context.Context, flags *initFlags) (err error) {
	// Create a new context that includes the azd access token
	ctx = azdext.WithAccessToken(ctx)

	// Create a new azd client
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}

	defer azdClient.Close()

	if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
			return nil
		}
		return fmt.Errorf("failed waiting for debugger: %w", err)
	}

	var extensionMetadata *models.ExtensionSchema
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current working directory: %w", err)
	}

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
					DefaultValue: new(true),
					Placeholder:  "yes",
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

	extensionPath := filepath.Join(cwd, extensionMetadata.Id)
	if info, err := os.Stat(extensionPath); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("a file named '%s' already exists", extensionMetadata.Id)
		}

		// Skip confirmation prompt in headless mode
		if !flags.noPrompt {
			nonEmpty, err := isDirNonEmpty(extensionPath)
			if err != nil {
				return fmt.Errorf("failed to inspect existing extension directory: %w", err)
			}

			message := fmt.Sprintf(
				"The extension directory '%s' already exists. Continue?",
				extensionMetadata.Id,
			)
			helpMessage := ""
			if nonEmpty {
				message = fmt.Sprintf(
					"The extension directory '%s' already exists and is not empty. "+
						"Existing files may be overwritten. Continue?",
					extensionMetadata.Id,
				)
				helpMessage = "Scaffolded files will overwrite any existing files at the same paths " +
					"(e.g. extension.yaml, main.go, README.md). Other files will be left untouched."
			}

			confirmResponse, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
				Options: &azdext.ConfirmOptions{
					Message:      message,
					DefaultValue: new(false),
					HelpMessage:  helpMessage,
				},
			})
			if err != nil {
				return fmt.Errorf("failed to confirm existing extension directory: %w", err)
			}

			if !*confirmResponse.Value {
				return errors.New("extension creation cancelled by user")
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to check extension directory: %w", err)
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
		if err := createExtensionDirectory(ctx, azdClient, extensionMetadata, cwd); err != nil {
			return ux.Error, common.NewDetailedError(
				"Error creating directory",
				fmt.Errorf("failed to create extension directory: %w", err),
			)
		}

		return ux.Success, nil
	}

	var buildWarnings []string
	// Ensure validation warnings are flushed after the live TaskList canvas
	// completes, regardless of whether a later task fails.
	defer func() { writeCollectedWarnings(os.Stdout, buildWarnings) }()

	validateExtensionAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
		warnings, validationErrors := validateExtensionMetadata(extensionMetadata)
		if len(validationErrors) > 0 {
			return ux.Error, validationFailureError(validationErrors)
		}

		if len(warnings) > 0 {
			buildWarnings = warnings
			return ux.Warning, fmt.Errorf("%s; see details below", validationWarningSummary(warnings))
		}

		return ux.Success, nil
	}

	runSubprocess := func(description string, args ...string) (ux.TaskState, error) {
		/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments */
		cmd := exec.Command("azd", args...)
		cmd.Dir = extensionMetadata.Path

		// Capture combined output so we can surface the child's own error message
		// inline in the wrapped error instead of letting the child's TaskList canvas
		// stream into our terminal alongside ours.
		result, err := cmd.CombinedOutput()
		if err != nil {
			return ux.Error, common.NewDetailedError(
				description,
				fmt.Errorf("%w%s", err, subprocessErrorTail(result)),
			)
		}

		return ux.Success, nil
	}

	buildExtensionAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
		return runSubprocess("Build failed", "x", "build", "--skip-install")
	}

	packageExtensionAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
		return runSubprocess("Package failed", "x", "pack")
	}

	publishExtensionAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
		return runSubprocess("Publish failed", "x", "publish")
	}

	installExtensionAction := func(spf ux.SetProgressFunc) (ux.TaskState, error) {
		return runSubprocess(
			"Install failed",
			"ext", "install", extensionMetadata.Id, "--source", "local",
		)
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
				Title:  "Validate extension metadata",
				Action: validateExtensionAction,
			}).
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

	if runErr := taskList.Run(); runErr != nil {
		err = fmt.Errorf("failed running init tasks: %w", runErr)
		return err
	}

	if localRegistryExists {
		fmt.Println("Local extension source already exists.")
		fmt.Println()
	}

	if !flags.createRegistry {
		fmt.Println(output.WithBold("Try out the extension"))
		fmt.Printf(
			"- Run %s to try your extension now.\n",
			output.WithHighLightFormat("azd %s -h", namespaceCommandPath(extensionMetadata.Namespace)),
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

	supportedNames := validCapabilityNames()
	for _, cap := range flags.capabilities {
		if !slices.Contains(extensions.ValidCapabilities, extensions.CapabilityType(cap)) {
			return nil, fmt.Errorf(
				"invalid capability '%s', supported capabilities are: %s",
				cap,
				strings.Join(supportedNames, ", "),
			)
		}
	}

	// Convert capabilities from string slice to CapabilityType slice
	capabilities := make([]extensions.CapabilityType, len(flags.capabilities))
	for i, cap := range flags.capabilities {
		capabilities[i] = extensions.CapabilityType(cap)
	}

	tags, err := parseTags(strings.Join(flags.tags, ","))
	if err != nil {
		return nil, err
	}

	// Set a default description
	description := "An azd extension"

	// Default namespace to ID if not provided
	namespace := flags.id
	if flags.namespace != "" {
		namespace = flags.namespace
	}
	if err := validateExtensionNamespace(namespace); err != nil {
		return nil, err
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
		Usage:        formatUsage(namespace),
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
			Hint: "Extension ID is used to identify your extension in the azd ecosystem. " +
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
			HelpMessage: "Display name is used to show the extension name in the azd CLI. " +
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
			Message:     "Enter tags for your extension (comma-separated, optional)",
			Placeholder: "tag1, tag2",
			HelpMessage: "Tags are used to categorize your extension. " +
				"You can enter multiple tags separated by commas, or leave empty to skip.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for tags: %w", err)
	}

	namespace, err := promptExtensionNamespace(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for namespace: %w", err)
	}

	capabilitiesPrompt, err := azdClient.Prompt().MultiSelect(ctx, &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message:         "Select capabilities for your extension",
			Choices:         capabilityPromptChoices(),
			EnableFiltering: new(false),
			DisplayNumbers:  new(false),
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
			EnableFiltering: new(false),
			DisplayNumbers:  new(false),
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

	tags, err := parseTags(tagsPrompt.Value)
	if err != nil {
		return nil, err
	}

	absExtensionPath, err := filepath.Abs(idPrompt.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for extension directory: %w", err)
	}

	return &models.ExtensionSchema{
		Id:           idPrompt.Value,
		DisplayName:  displayNamePrompt.Value,
		Description:  descriptionPrompt.Value,
		Namespace:    namespace,
		Capabilities: capabilities,
		Language:     languageChoices[*programmingLanguagePrompt.Value].Value,
		Tags:         tags,
		Usage:        formatUsage(namespace),
		Version:      "0.0.1",
		Path:         absExtensionPath,
	}, nil
}

func validCapabilityNames() []string {
	names := make([]string, len(extensions.ValidCapabilities))
	for i, cap := range extensions.ValidCapabilities {
		names[i] = string(cap)
	}

	return names
}

// namespaceCommandPath converts an extension namespace (e.g. "ai.project") into the
// command path used to invoke it from azd (e.g. "ai project"). Dots in a namespace
// represent nested command groups; see bindExtension in cli/azd/cmd/extensions.go.
func namespaceCommandPath(namespace string) string {
	return strings.ReplaceAll(namespace, ".", " ")
}

// formatUsage returns the usage hint string for an extension with the given namespace,
// translating dotted namespaces into the equivalent nested-command form.
func formatUsage(namespace string) string {
	return fmt.Sprintf("azd %s <command> [options]", namespaceCommandPath(namespace))
}

func validateExtensionNamespace(namespace string) error {
	if !extensionNamespacePattern.MatchString(namespace) {
		return fmt.Errorf(
			"invalid namespace '%s': use lowercase letters, numbers, and hyphens separated by single dots "+
				"(for example, 'foo.bar' or 'coding-agent')",
			namespace,
		)
	}

	return nil
}

func promptExtensionNamespace(ctx context.Context, azdClient *azdext.AzdClient) (string, error) {
	for {
		namespacePrompt, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:         "Enter a namespace for your extension",
				Placeholder:     "foo.bar",
				RequiredMessage: "Namespace is required",
				Required:        true,
				HelpMessage: "Namespace is used to group custom commands into a single command " +
					"group used for executing the extension. " +
					"Use dots to create nested command groups (e.g. 'foo.bar' becomes 'azd foo bar'). " +
					"Use only lowercase letters, numbers, and hyphens separated by single dots; spaces are not allowed.",
			},
		})
		if err != nil {
			return "", err
		}

		if err := validateExtensionNamespace(namespacePrompt.Value); err != nil {
			fmt.Println(output.WithErrorFormat(err.Error()))
			continue
		}

		return namespacePrompt.Value, nil
	}
}

func parseTags(rawTags string) ([]string, error) {
	var tags []string
	for tag := range strings.SplitSeq(rawTags, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}

		if len(tags) == maxExtensionTags {
			return nil, fmt.Errorf("too many tags: maximum is %d", maxExtensionTags)
		}
		if len(tag) > maxExtensionTagLength {
			return nil, fmt.Errorf("tag '%s' is too long: maximum length is %d", tag, maxExtensionTagLength)
		}
		if strings.ContainsFunc(tag, unicode.IsControl) {
			return nil, fmt.Errorf("tag '%s' contains control characters", tag)
		}

		tags = append(tags, tag)
	}

	return tags, nil
}

func capabilityPromptChoices() []*azdext.MultiSelectChoice {
	choices := make([]*azdext.MultiSelectChoice, len(extensions.ValidCapabilities))
	for i, cap := range extensions.ValidCapabilities {
		choices[i] = &azdext.MultiSelectChoice{
			Label: capabilityLabel(cap),
			Value: string(cap),
		}
	}

	return choices
}

func capabilityLabel(cap extensions.CapabilityType) string {
	words := strings.Split(string(cap), "-")
	for i, word := range words {
		if word == "" {
			continue
		}

		switch strings.ToLower(word) {
		case "mcp":
			words[i] = "MCP"
		default:
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}

	return strings.Join(words, " ")
}

// isDirNonEmpty reports whether dir contains at least one entry. It returns
// (false, nil) for an empty directory and propagates the underlying error
// otherwise. Implemented via Readdirnames(1) to avoid reading the entire
// directory listing into memory.
func isDirNonEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()

	names, err := f.Readdirnames(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return len(names) > 0, nil
}

func createExtensionDirectory(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	extensionMetadata *models.ExtensionSchema,
	cwd string,
) error {
	extensionPath := filepath.Join(cwd, extensionMetadata.Id)

	info, err := os.Stat(extensionPath)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("a file named '%s' already exists", extensionMetadata.Id)
	}

	if os.IsNotExist(err) {
		if err := os.MkdirAll(extensionPath, internal.PermissionDirectory); err != nil {
			return fmt.Errorf("failed to create extension directory: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check extension directory: %w", err)
	}
	// If directory already exists (err == nil), continue to create/update files

	// Create project from template.
	namespaceParts := strings.Split(extensionMetadata.Namespace, ".")
	templateMetadata := &ExtensionTemplate{
		Metadata:      extensionMetadata,
		LeafNamespace: namespaceParts[len(namespaceParts)-1],
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
	yamlContents := append([]byte(extensionSchemaHeader), yamlBytes...)
	if err := os.WriteFile(extensionFilePath, yamlContents, internal.PermissionFile); err != nil {
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
			tmpl, err := template.New(filepath.Base(path)).Funcs(templateFuncs).Parse(string(fileBytes))
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

// writeCollectedWarnings prints collected validation warnings after the task list canvas is complete.
func writeCollectedWarnings(writer io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}

	fmt.Fprintln(writer, output.WithWarningFormat("Validation warnings:"))
	for _, warning := range warnings {
		fmt.Fprintf(writer, "  - %s\n", warning)
	}
	fmt.Fprintln(writer)
}

// ansiEscapeRegex matches ANSI CSI escape sequences and OSC hyperlinks commonly
// emitted by child azd processes.
var ansiEscapeRegex = regexp.MustCompile(`(?:\x1b\[[0-9;]*[A-Za-z])|(?:\x1b\][^\x07\x1b]*(?:\x07|\x1b\\))`)

// subprocessErrorTail extracts a short, human-friendly summary line from captured
// subprocess output to inline into a wrapped error message. It prefers the first
// line beginning with "ERROR:"/"Error:" and falls back to the last non-empty line.
// The returned string is prefixed with ": " when non-empty, or empty otherwise.
func subprocessErrorTail(output []byte) string {
	if len(output) == 0 {
		return ""
	}

	cleaned := ansiEscapeRegex.ReplaceAllString(string(output), "")

	var fallback string
	for line := range strings.SplitSeq(cleaned, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "ERROR:") || strings.HasPrefix(trimmed, "Error:") {
			errorLine := strings.TrimSpace(
				strings.TrimPrefix(strings.TrimPrefix(trimmed, "ERROR:"), "Error:"),
			)
			if errorLine == "" {
				continue
			}

			return ": " + errorLine
		}
		fallback = trimmed
	}

	if fallback == "" {
		return ""
	}
	return ": " + fallback
}

// ExtensionTemplate contains values used when rendering extension project templates.
type ExtensionTemplate struct {
	Metadata *models.ExtensionSchema
	// LeafNamespace is the final dot-separated segment of Metadata.Namespace, used as the
	// cobra Use/Name for the extension's root command. For nested namespaces like
	// "ai.agents", users invoke the extension via "azd ai agents" (azd splits on '.'),
	// so the extension's own root command name is the leaf ("agents").
	LeafNamespace string
	DotNet        *DotNetTemplate
}

// templateFuncs are template helpers exposed to .tmpl files when rendering
// extension scaffolds. They allow user-supplied strings (e.g. extension
// description) to be safely embedded in generated source code.
var templateFuncs = template.FuncMap{
	// goString quotes a string as a Go double-quoted literal, escaping any
	// characters that would otherwise produce invalid Go source (quotes,
	// backslashes, newlines, control characters, etc.). The returned value
	// includes the surrounding quotes.
	"goString": strconv.Quote,
}

type DotNetTemplate struct {
	Namespace string
	ExeName   string
}
