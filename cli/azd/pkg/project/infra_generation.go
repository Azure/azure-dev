// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/yamlnode"
	"github.com/braydonk/yaml"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// InfraGenerationProviderKey returns the IoC key used to register the infrastructure generator for provider.
// The key is distinct from the provisioning provider key so generation can be resolved independently.
func InfraGenerationProviderKey(provider string) string {
	return "infra-generation:" + provider
}

// InfrastructureGenerator is implemented by providers (typically extensions) that can write infrastructure
// source files for a single infrastructure entry.
type InfrastructureGenerator interface {
	// Generate writes source files for the requested entry and returns the replacement entry that should be
	// saved to the project file.
	Generate(context.Context, *v1beta.InfraGenerateRequest) (*v1beta.InfraGenerateResponse, error)
}

// InfraGenerationManager supports legacy infrastructure generation and extension-based generation.
type InfraGenerationManager struct {
	// importer generates infrastructure for the built-in providers.
	importer *ImportManager

	// locator resolves extension generators registered under InfraGenerationProviderKey.
	locator ioc.ServiceLocator

	// azdContext identifies the project file loaded by azd.
	azdContext *azdcontext.AzdContext
}

// NewInfraGenerationManager creates an InfraGenerationManager.
//   - importer generates infrastructure for the built-in providers.
//   - locator resolves extension generators.
//   - azdContext identifies the project file.
func NewInfraGenerationManager(
	importer *ImportManager, locator ioc.ServiceLocator, azdContext *azdcontext.AzdContext,
) *InfraGenerationManager {
	return &InfraGenerationManager{importer: importer, locator: locator, azdContext: azdContext}
}

// generatedInfraEntry tracks a single extension-backed infrastructure entry through generation and saving.
type generatedInfraEntry struct {
	// provider is the extension generator that handles this entry.
	provider InfrastructureGenerator

	// request is the request sent to provider.
	request *v1beta.InfraGenerateRequest

	// identity is the entry as loaded from the project, used to locate it again in the project file.
	identity provisioning.Options

	// format identifies the persisted project layout used to locate the entry.
	format ProjectFormat

	// original is the marshaled YAML node for this entry, captured before generation. Saving fails if
	// the project file no longer matches it, so we never overwrite concurrent edits.
	original []byte

	// replacement is the entry returned by provider. It's nil until the generator has run.
	replacement *v1beta.ProvisioningOptions
}

// GenerateLegacy generates infrastructure through the legacy importer for projects without extension providers.
// The caller is responsible for writing the returned files.
func (m *InfraGenerationManager) GenerateLegacy(ctx context.Context, config *ProjectConfig) (fs.FS, error) {
	if slices.ContainsFunc(config.InfrastructureConfigs(), isExtensionInfraEntry) {
		return nil, fmt.Errorf("legacy infrastructure generation cannot be used with extension providers")
	}

	return m.importer.GenerateAllInfrastructure(ctx, config)
}

// GenerateExtensions delegates extension-backed infrastructure entries to their generators and saves the replacement
// entries to the project file. Extension entries must have distinct output paths.
func (m *InfraGenerationManager) GenerateExtensions(
	ctx context.Context, config *ProjectConfig, options *v1beta.InfraGenerateOptions,
) error {
	if options == nil {
		return fmt.Errorf("infrastructure generation requires a non-nil options message")
	}

	// TODO: this first pass just assumes we want to convert _everything_, but I'd like to change
	// it to allow you to select specific layers to convert.
	entries := config.InfrastructureConfigs()
	if !slices.ContainsFunc(entries, isExtensionInfraEntry) {
		return fmt.Errorf("extension infrastructure generation requires at least one extension provider")
	}

	if len(config.Resources) > 0 {
		return fmt.Errorf("generation cannot combine a resources section with extension infrastructure entries")
	}

	projectFile := m.azdContext.ProjectPath()

	document, err := readGenerationDocument(projectFile)
	if err != nil {
		return err
	}

	var pending []generatedInfraEntry
	outputPaths := make(map[string]string)

	for _, entry := range entries {
		if !isExtensionInfraEntry(entry) {
			continue
		}

		prepared, err := m.prepareEntry(config, document, entry, options)
		if err != nil {
			return err
		}

		outputPath := prepared.request.OutputPath
		if previous, exists := outputPaths[outputPath]; exists {
			return fmt.Errorf(
				"infrastructure entries %q and %q share output path %q; configure a distinct path for each entry",
				previous, entry.Name, outputPath)
		}
		outputPaths[outputPath] = entry.Name

		pending = append(pending, prepared)
	}

	if err := runGenerators(ctx, pending); err != nil {
		return err
	}

	if err := saveGeneratedEntries(ctx, config, projectFile, pending); err != nil {
		return fmt.Errorf("infrastructure files were generated, but updating project configuration failed: %w", err)
	}

	return nil
}

// ExtensionOutputPaths returns the folders extension generators write into for config.
// Paths are project-relative unless the project configures an absolute infra path.
func (m *InfraGenerationManager) ExtensionOutputPaths(config *ProjectConfig) []string {
	infraEntries := config.InfrastructureConfigs()

	var paths []string

	for _, entry := range infraEntries {
		if isExtensionInfraEntry(entry) {
			paths = append(paths, generationOutputPath(entry))
		}
	}

	return paths
}

// generationOutputPath returns the cleaned, forward-slash-separated, project-relative folder an extension
// entry generates into.
func generationOutputPath(entry provisioning.Options) string {
	if entry.Path != "" {
		return path.Clean(filepath.ToSlash(entry.Path))
	}

	if entry.Name == "" {
		return "infra"
	}

	return path.Join("infra", sanitizeGenerationDirectoryName(entry.Name))
}

// The range \x00-\x1f covers ASCII control characters, including NUL, tab, and newline.
var invalidGenerationDirectoryCharacters = regexp.MustCompile(`[\x00-\x1f<>:"/\\|?*]`)

// sanitizeGenerationDirectoryName replaces characters unsuitable for a directory name with underscores.
func sanitizeGenerationDirectoryName(name string) string {
	name = invalidGenerationDirectoryCharacters.ReplaceAllString(name, "_")
	name = strings.TrimRight(name, ". ")
	if name == "" {
		return "_"
	}

	// IsLocal rejects Windows device names only on Windows, so other platforms leave those names unchanged.
	if !filepath.IsLocal(name) {
		return "_" + name
	}

	return name
}

// isExtensionInfraEntry returns true if entry uses a provider that isn't built into azd.
func isExtensionInfraEntry(entry provisioning.Options) bool {
	return entry.Provider != provisioning.NotSpecified &&
		!slices.Contains(provisioning.BuiltInProviderKinds(), entry.Provider)
}

// prepareEntry resolves the generator for entry, verifies that the project file still matches the loaded project,
// and builds the generation request.
func (m *InfraGenerationManager) prepareEntry(
	config *ProjectConfig, document *yaml.Node, entry provisioning.Options, options *v1beta.InfraGenerateOptions,
) (generatedInfraEntry, error) {
	provider, err := m.resolveGenerator(entry.Provider)
	if err != nil {
		return generatedInfraEntry{}, err
	}

	outputPath := generationOutputPath(entry)

	if !validGenerationPath(outputPath) {
		return generatedInfraEntry{}, fmt.Errorf(
			"generated infrastructure output path %q must be project-relative", outputPath)
	}

	var entryConfig *structpb.Struct

	if entry.Config != nil {
		entryConfig, err = structpb.NewStruct(entry.Config)
		if err != nil {
			return generatedInfraEntry{}, fmt.Errorf("encoding infrastructure entry %q: %w", entry.Name, err)
		}
	}

	// Paths are compared and sent to extensions in slash form so the result doesn't depend on the host OS.
	entry.Path = filepath.ToSlash(entry.Path)

	format := config.Format()
	original, err := captureUnchangedEntry(config, document, entry, format)
	if err != nil {
		return generatedInfraEntry{}, err
	}

	return generatedInfraEntry{
		provider: provider,
		identity: entry,
		format:   format,
		original: original,
		request: &v1beta.InfraGenerateRequest{
			ProjectPath: config.Path,
			OutputPath:  outputPath,
			Options:     proto.Clone(options).(*v1beta.InfraGenerateOptions),
			Entry: &v1beta.ProvisioningOptions{
				Provider:              string(entry.Provider),
				Name:                  entry.Name,
				Config:                entryConfig,
				Path:                  entry.Path,
				Module:                entry.Module,
				IgnoreDeploymentState: entry.IgnoreDeploymentState,
				VirtualEnv:            maps.Clone(entry.VirtualEnv),
			},
		},
	}, nil
}

// resolveGenerator returns the registered InfrastructureGenerator for provider.
func (m *InfraGenerationManager) resolveGenerator(provider provisioning.ProviderKind) (InfrastructureGenerator, error) {
	if m.locator == nil {
		return nil, fmt.Errorf("infrastructure generation provider %q is not available", provider)
	}

	var generator InfrastructureGenerator

	if err := m.locator.ResolveNamed(InfraGenerationProviderKey(string(provider)), &generator); err != nil {
		return nil, fmt.Errorf(
			"infrastructure provider %q does not have an available infra-generation-provider capability: %w",
			provider, err)
	}

	return generator, nil
}

// captureUnchangedEntry checks that the entry in document still matches the loaded entry, and returns the
// entry's marshaled YAML node so saving can later detect concurrent edits.
func captureUnchangedEntry(
	config *ProjectConfig, document *yaml.Node, entry provisioning.Options, format ProjectFormat,
) ([]byte, error) {
	node, err := findGenerationEntry(document, entry, format)
	if err != nil {
		return nil, err
	}

	var current provisioning.Options
	if err := node.Decode(&current); err != nil {
		return nil, fmt.Errorf("decoding infrastructure entry %q: %w", entry.Name, err)
	}

	// Normalize the on-disk entry the same way project loading does, so the comparison below only fails on
	// real edits.
	current.Path = strings.ReplaceAll(current.Path, "\\", "/")

	current.Provider, err = provisioning.ParseProvider(current.Provider)
	if err != nil {
		return nil, err
	}

	if current.Provider == provisioning.NotSpecified && entry.Layer == "" {
		current.Provider = config.Infra.Provider
	}

	loaded, err := yaml.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshaling loaded infrastructure entry %q: %w", entry.Name, err)
	}

	actual, err := yaml.Marshal(current)
	if err != nil {
		return nil, fmt.Errorf("marshaling current infrastructure entry %q: %w", entry.Name, err)
	}

	if !bytes.Equal(loaded, actual) {
		return nil, fmt.Errorf("infrastructure entry %q changed since the project was loaded", entry.Name)
	}

	original, err := yaml.Marshal(node)
	if err != nil {
		return nil, fmt.Errorf("marshaling infrastructure entry %q: %w", entry.Name, err)
	}

	return original, nil
}

// runGenerators invokes each entry's generator and stores the validated replacement entry on it.
func runGenerators(ctx context.Context, pending []generatedInfraEntry) error {
	for i := range pending {
		entry := &pending[i]
		name := entry.request.Entry.Name

		if err := ctx.Err(); err != nil {
			return err
		}

		// Clone so a generator can't mutate the request we use later for error messages.
		response, err := entry.provider.Generate(ctx, proto.Clone(entry.request).(*v1beta.InfraGenerateRequest))
		if err != nil {
			return fmt.Errorf("generating infrastructure entry %q: %w", name, err)
		}

		if err := validateGeneratedEntry(name, response); err != nil {
			return err
		}

		entry.replacement = proto.Clone(response.Entry).(*v1beta.ProvisioningOptions)
	}

	return nil
}

// validGenerationPath returns true if value is a non-empty, project-relative, slash-separated path that
// doesn't escape the project directory.
func validGenerationPath(value string) bool {
	return fs.ValidPath(value) && value != "." && filepath.IsLocal(value) && !strings.Contains(value, "\\")
}

// validateGeneratedEntry returns an error if response doesn't contain a replacement entry that azd can safely
// save in place of the entry called name.
func validateGeneratedEntry(name string, response *v1beta.InfraGenerateResponse) error {
	if response == nil || response.Entry == nil || strings.TrimSpace(response.Entry.Provider) == "" {
		return fmt.Errorf("infrastructure generator for %q returned no replacement provider entry", name)
	}

	entry := response.Entry

	if entry.Name != "" && entry.Name != name {
		return fmt.Errorf("replacement infrastructure entry %q cannot rename entry %q", entry.Name, name)
	}

	if (entry.Path != "" && !validGenerationPath(entry.Path)) ||
		(entry.Module != "" && !validGenerationPath(entry.Module)) {
		return fmt.Errorf("replacement infrastructure entry %q contains an invalid path or module", name)
	}

	if entry.IgnoreDeploymentState || len(entry.VirtualEnv) > 0 || len(entry.DeploymentStacks) > 0 {
		return fmt.Errorf(
			"replacement infrastructure entry %q contains unsupported runtime or deployment stack options", name)
	}

	return nil
}

// readGenerationDocument reads and parses the project file, preserving comments and formatting.
func readGenerationDocument(file string) (*yaml.Node, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading project for infrastructure generation: %w", err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("reading project YAML: %w", err)
	}

	return &document, nil
}

// findGenerationEntry returns the YAML node for entry within document.
func findGenerationEntry(
	document *yaml.Node, entry provisioning.Options, format ProjectFormat,
) (*yaml.Node, error) {
	nodePath, err := generationEntryPath(document, entry, format)
	if err != nil {
		return nil, err
	}

	return yamlnode.Find(document, nodePath)
}

// generationEntryPath returns the yamlnode path to entry within document.
func generationEntryPath(document *yaml.Node, entry provisioning.Options, format ProjectFormat) (string, error) {
	var sequencePath string

	switch format {
	case ProjectFormatFlat:
		// A flat infra mapping can also select an extension provider, with or without a name.
		return "infra", nil

	case ProjectFormatInfraV1:
		sequencePath = "infra.layers"
		sequence, err := yamlnode.Find(document, "infra.layers")
		if err != nil {
			return "", err
		}
		if len(sequence.Content) == 0 {
			// GetLayers falls back to the root infra entry when the layers list is empty.
			return "infra", nil
		}

	case ProjectFormatLayersV2:
		// Entry names are located within their project layer, not across the whole project.
		layers, err := yamlnode.Find(document, "layers")
		if err != nil {
			return "", err
		}

		for i, layer := range layers.Content {
			name, err := yamlnode.Find(layer, "name")
			if err == nil && name.Value == entry.Layer {
				sequencePath = fmt.Sprintf("layers[%d].infra", i)
				break
			}
		}

		if sequencePath == "" {
			return "", fmt.Errorf("project layer %q was not found", entry.Layer)
		}

	default:
		return "", fmt.Errorf("unsupported project format %d", format)
	}

	sequence, err := yamlnode.Find(document, sequencePath)
	if err != nil {
		return "", err
	}

	for i, node := range sequence.Content {
		name, err := yamlnode.Find(node, "name")
		if err == nil && name.Value == entry.Name {
			return fmt.Sprintf("%s[%d]", sequencePath, i), nil
		}
	}

	return "", fmt.Errorf("infrastructure entry %q was not found", entry.Name)
}

// saveGeneratedEntries writes each entry's replacement to file and refreshes config's infrastructure from the
// result. It fails without writing if any entry changed on disk during generation, or if the updated
// project doesn't validate.
func saveGeneratedEntries(
	ctx context.Context, config *ProjectConfig, file string, entries []generatedInfraEntry,
) error {
	document, err := readGenerationDocument(file)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if err := replaceGeneratedEntry(document, entry); err != nil {
			return err
		}
	}

	output, err := encodeGenerationDocument(document)
	if err != nil {
		return err
	}

	updated, err := Parse(ctx, string(output))
	if err != nil {
		return fmt.Errorf("validating generated project configuration: %w", err)
	}

	if err := updated.Validate(); err != nil {
		return fmt.Errorf("validating generated project configuration: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := azdext.WriteFileAtomic(file, output, 0); err != nil {
		return fmt.Errorf("writing project file: %w", err)
	}

	refreshInfraConfig(config, updated)
	return nil
}

// replaceGeneratedEntry updates entry's node in document with its replacement, after checking that the node
// hasn't changed since generation started.
func replaceGeneratedEntry(document *yaml.Node, entry generatedInfraEntry) error {
	name := entry.request.Entry.Name

	node, err := findGenerationEntry(document, entry.identity, entry.format)
	if err != nil {
		return err
	}

	current, err := yaml.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshaling infrastructure entry %q: %w", name, err)
	}

	if !bytes.Equal(current, entry.original) {
		return fmt.Errorf("infrastructure entry %q changed during generation", name)
	}

	values := replacementValues(entry.replacement)

	if err := replaceExistingFields(node, values); err != nil {
		return fmt.Errorf("updating infrastructure entry %q: %w", name, err)
	}

	// Any values left over weren't already present in the entry, so append them in a stable order.
	for _, key := range slices.Sorted(maps.Keys(values)) {
		encoded, err := yamlnode.Encode(values[key])
		if err != nil {
			return fmt.Errorf("encoding %q for infrastructure entry %q: %w", key, name, err)
		}

		if err := yamlnode.Set(node, key, encoded); err != nil {
			return fmt.Errorf("setting %q for infrastructure entry %q: %w", key, name, err)
		}
	}

	return nil
}

// replacementValues returns the azure.yaml fields, keyed by YAML field name, that replacement sets.
func replacementValues(replacement *v1beta.ProvisioningOptions) map[string]any {
	values := map[string]any{"provider": replacement.Provider}

	if replacement.Path != "" {
		values["path"] = replacement.Path
	}

	if replacement.Module != "" {
		values["module"] = replacement.Module
	}

	if replacement.Config != nil {
		values["config"] = replacement.Config.AsMap()
	}

	return values
}

// replaceExistingFields rewrites the replaceable fields (provider, path, module, config) of the mapping node.
// Fields with a value in values are updated in place, keeping their comments, and fields without one are
// removed. Each value that's applied is deleted from values, leaving only the fields the node didn't have.
func replaceExistingFields(node *yaml.Node, values map[string]any) error {
	var fields []*yaml.Node

	// Mapping node content alternates key, value.
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]

		switch key.Value {
		case "provider", "path", "module", "config":
			newValue, exists := values[key.Value]
			if !exists {
				continue
			}

			encoded, err := yamlnode.Encode(newValue)
			if err != nil {
				return fmt.Errorf("encoding %q: %w", key.Value, err)
			}

			encoded.HeadComment, encoded.LineComment, encoded.FootComment =
				value.HeadComment, value.LineComment, value.FootComment
			value = encoded

			delete(values, key.Value)
		}

		fields = append(fields, key, value)
	}

	node.Content = fields
	return nil
}

// encodeGenerationDocument serializes document using azure.yaml's two-space indentation.
func encodeGenerationDocument(document *yaml.Node) ([]byte, error) {
	var output bytes.Buffer

	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)

	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("encoding project YAML: %w", err)
	}

	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("encoding project YAML: %w", err)
	}

	return output.Bytes(), nil
}

// refreshInfraConfig copies the saved infrastructure configuration from updated into config, so callers see
// the replacement entries without reloading the project.
func refreshInfraConfig(config *ProjectConfig, updated *ProjectConfig) {
	if config.Format() != ProjectFormatLayersV2 {
		config.Infra = updated.Infra
		return
	}

	for _, layer := range config.Layers {
		for _, saved := range updated.Layers {
			if layer.Name == saved.Name {
				layer.Infra = saved.Infra
			}
		}
	}
}
