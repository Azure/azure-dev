// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/provisioning"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
)

const (
	projectDefaultInfraPath   = "infra"
	projectDefaultInfraModule = "main"
	projectFoundryLayerName   = "foundry"
	projectFoundryLayerPath   = "infra/foundry"
	projectTerraformMarker    = ".azd-foundry"
	projectTerraformMarkerV1  = "terraform-v1\n"
)

type projectInfraEjectPlan struct {
	targetDir         string
	targetPath        string
	module            string
	layer             bool
	mergeExisting     bool
	updatedYAML       []byte
	updateDescription string
}

type projectInfraTarget struct {
	dir    string
	exists bool
	empty  bool
}

type projectInfraConfig struct {
	root         *yaml.Node
	infra        *yaml.Node
	layersNode   *yaml.Node
	rootProvider string
	layers       []projectInfraLayer
}

type projectInfraLayer struct {
	node              *yaml.Node
	name              string
	path              string
	module            string
	provider          string
	effectiveProvider string
}

func planProjectInfraEject(
	projectRoot string,
	rawYAML []byte,
	provider string,
) (*projectInfraEjectPlan, error) {
	if provider != provisioning.BicepProviderName &&
		provider != provisioning.TerraformProviderName {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unsupported infrastructure provider %q", provider),
			"choose bicep or terraform for --infra",
		)
	}
	config, document, err := parseProjectInfraConfig(rawYAML)
	if err != nil {
		return nil, err
	}
	infraProvider := projectFoundryProvider(provider)
	if config.layersNode != nil {
		return planProjectLayeredInfra(
			projectRoot, &document, config, infraProvider,
		)
	}
	return planProjectRootInfra(
		projectRoot, &document, config, infraProvider,
	)
}

func parseProjectInfraConfig(
	rawYAML []byte,
) (*projectInfraConfig, yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(rawYAML, &document); err != nil {
		return nil, yaml.Node{}, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("parse azure.yaml: %s", err),
			"verify azure.yaml is valid YAML",
		)
	}
	if len(document.Content) == 0 ||
		document.Content[0].Kind != yaml.MappingNode {
		return nil, yaml.Node{}, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"azure.yaml is not a YAML mapping at the top level",
			"verify azure.yaml is a valid azd project file",
		)
	}

	root := document.Content[0]
	infra := yamlMappingValue(root, "infra")
	if infra == nil {
		infra = yamlMappingNode()
		root.Content = append(root.Content, yamlScalarNode("infra"), infra)
	}
	if infra.Kind != yaml.MappingNode {
		return nil, yaml.Node{}, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"infra must be a mapping",
			"fix the infra section in azure.yaml and retry",
		)
	}
	for _, key := range []string{"name", "path", "module", "provider"} {
		if value := yamlMappingValue(infra, key); value != nil &&
			value.Kind != yaml.ScalarNode {
			return nil, yaml.Node{}, exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				fmt.Sprintf("infra.%s must be a string", key),
				"fix the infra section in azure.yaml and retry",
			)
		}
	}

	layersNode := yamlMappingValue(infra, "layers")
	config := &projectInfraConfig{
		root:         root,
		infra:        infra,
		layersNode:   layersNode,
		rootProvider: yamlMappingScalar(infra, "provider"),
	}
	if layersNode == nil {
		return config, document, nil
	}
	if layersNode.Kind != yaml.SequenceNode {
		return nil, yaml.Node{}, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"infra.layers must be a sequence",
			"fix the infra section in azure.yaml and retry",
		)
	}

	names := make(map[string]struct{}, len(layersNode.Content))
	paths := make(map[string]string, len(layersNode.Content))
	for _, node := range layersNode.Content {
		if node.Kind != yaml.MappingNode {
			return nil, yaml.Node{}, exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				"each infra.layers entry must be a mapping",
				"fix the infra section in azure.yaml and retry",
			)
		}
		for _, key := range []string{"name", "path", "module", "provider"} {
			if value := yamlMappingValue(node, key); value != nil &&
				value.Kind != yaml.ScalarNode {
				return nil, yaml.Node{}, exterrors.Validation(
					exterrors.CodeInvalidAzureYaml,
					fmt.Sprintf("infra.layers[].%s must be a string", key),
					"fix the infra section in azure.yaml and retry",
				)
			}
		}
		name := yamlMappingScalar(node, "name")
		path := yamlMappingScalar(node, "path")
		if name == "" || path == "" {
			return nil, yaml.Node{}, exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				"each infra.layers entry needs name and path",
				"set name and path on every infrastructure layer",
			)
		}
		if _, exists := names[name]; exists {
			return nil, yaml.Node{}, exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				fmt.Sprintf("infrastructure layer %q is duplicated", name),
				"give each infrastructure layer a unique name",
			)
		}
		names[name] = struct{}{}

		cleanPath := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
		if previous, exists := paths[cleanPath]; exists {
			return nil, yaml.Node{}, exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				fmt.Sprintf(
					"infrastructure layers %q and %q use path %q",
					previous, name, path,
				),
				"use a unique path for every infrastructure layer",
			)
		}
		paths[cleanPath] = name

		layerProvider := yamlMappingScalar(node, "provider")
		effectiveProvider := layerProvider
		if effectiveProvider == "" {
			effectiveProvider = config.rootProvider
		}
		config.layers = append(config.layers, projectInfraLayer{
			node:              node,
			name:              name,
			path:              path,
			module:            yamlMappingScalar(node, "module"),
			provider:          layerProvider,
			effectiveProvider: effectiveProvider,
		})
	}
	return config, document, nil
}

func planProjectRootInfra(
	projectRoot string,
	document *yaml.Node,
	config *projectInfraConfig,
	infraProvider string,
) (*projectInfraEjectPlan, error) {
	rootPath := yamlMappingScalar(config.infra, "path")
	if rootPath == "" {
		rootPath = projectDefaultInfraPath
	}
	rootModule := yamlMappingScalar(config.infra, "module")
	if rootModule == "" {
		rootModule = projectDefaultInfraModule
	}
	target, err := inspectProjectInfraTarget(projectRoot, rootPath)
	if err != nil {
		return nil, err
	}
	userOwned, err := projectRootInfraUserOwned(
		target, rootPath, config.rootProvider, rootModule,
	)
	if err != nil {
		return nil, err
	}
	if !userOwned {
		var updated []byte
		if config.rootProvider != infraProvider {
			yamlSetMappingScalar(config.infra, "provider", infraProvider)
			updated, err = marshalProjectAzureYAML(document)
			if err != nil {
				return nil, err
			}
		}
		return newProjectInfraEjectPlan(
			projectRoot, rootPath, rootModule, false,
			target.exists && target.empty, updated,
		)
	}

	existingName := yamlMappingScalar(config.infra, "name")
	if existingName == "" {
		existingName = projectDefaultInfraPath
	}
	if sameProjectInfraPath(rootPath, projectFoundryLayerPath) {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf(
				"existing infrastructure already uses the Foundry layer path %q",
				projectFoundryLayerPath,
			),
			"set infra.path to a different project-relative path",
		)
	}
	if existingName == projectFoundryLayerName {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf(
				"existing infrastructure name %q conflicts with the Foundry layer name",
				existingName,
			),
			"set infra.name to a different layer name",
		)
	}
	existingProvider := config.rootProvider
	if existingProvider == "" {
		existingProvider = provisioning.BicepProviderName
	}
	existingLayer, err := cloneProjectYAMLNode(config.infra)
	if err != nil {
		return nil, err
	}
	yamlSetMappingScalar(existingLayer, "name", existingName)
	yamlSetMappingScalar(existingLayer, "path", filepath.ToSlash(rootPath))
	yamlSetMappingScalar(existingLayer, "provider", existingProvider)
	yamlRemoveMappingKey(existingLayer, "layers")

	foundryLayer := newProjectInfraLayerNode(
		projectFoundryLayerName, projectFoundryLayerPath, infraProvider,
	)
	config.infra.Content = []*yaml.Node{
		yamlScalarNode("layers"),
		{
			Kind:    yaml.SequenceNode,
			Tag:     "!!seq",
			Content: []*yaml.Node{existingLayer, foundryLayer},
		},
	}
	updated, err := marshalProjectAzureYAML(document)
	if err != nil {
		return nil, err
	}
	foundryTarget, err := inspectProjectInfraTarget(
		projectRoot, projectFoundryLayerPath,
	)
	if err != nil {
		return nil, err
	}
	return newProjectInfraEjectPlan(
		projectRoot, projectFoundryLayerPath, "", true,
		foundryTarget.exists, updated,
	)
}

func planProjectLayeredInfra(
	projectRoot string,
	document *yaml.Node,
	config *projectInfraConfig,
	infraProvider string,
) (*projectInfraEjectPlan, error) {
	var foundry *projectInfraLayer
	for i := range config.layers {
		layer := &config.layers[i]
		effective := layer.effectiveProvider
		if effective == "" {
			effective = provisioning.BicepProviderName
		}
		if layer.name == projectFoundryLayerName {
			if foundry != nil {
				return nil, exterrors.Validation(
					exterrors.CodeInvalidAzureYaml,
					"azure.yaml has multiple Foundry layers",
					"keep only one layer named foundry",
				)
			}
			foundry = layer
			if layer.provider == "" {
				return nil, exterrors.Validation(
					exterrors.CodeInvalidAzureYaml,
					"the Foundry layer must declare provider",
					"set provider on the foundry layer",
				)
			}
			if layer.provider != infraProvider {
				return nil, exterrors.Validation(
					"infra_provider_conflict",
					fmt.Sprintf(
						"Foundry layer %q already uses provider %q",
						projectFoundryLayerName, layer.provider,
					),
					"keep the existing provider or change --infra to match it",
				)
			}
		} else if effective == provisioning.FoundryProviderName {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				fmt.Sprintf(
					"Foundry infrastructure already exists as layer %q",
					layer.name,
				),
				"keep the Foundry infrastructure in the layer named foundry",
			)
		}
		layer.effectiveProvider = effective
	}

	if foundry == nil {
		for i := range config.layers {
			if sameProjectInfraPath(
				config.layers[i].path, projectFoundryLayerPath,
			) {
				return nil, exterrors.Validation(
					exterrors.CodeInvalidAzureYaml,
					fmt.Sprintf(
						"infra layer %q already uses the Foundry layer path %q",
						config.layers[i].name, projectFoundryLayerPath,
					),
					"set the Foundry layer path to a unique project-relative directory",
				)
			}
		}
		config.layersNode.Content = append(
			config.layersNode.Content,
			newProjectInfraLayerNode(
				projectFoundryLayerName, projectFoundryLayerPath, infraProvider,
			),
		)
		updated, err := marshalProjectAzureYAML(document)
		if err != nil {
			return nil, err
		}
		target, err := inspectProjectInfraTarget(projectRoot, projectFoundryLayerPath)
		if err != nil {
			return nil, err
		}
		plan, err := newProjectInfraEjectPlan(
			projectRoot, projectFoundryLayerPath, "", true,
			target.exists, updated,
		)
		return plan, err
	}
	if len(config.layers) == 1 {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"infra.layers contains only a Foundry layer; use a root infra configuration for a Foundry-only project",
			"remove infra.layers or add an existing infrastructure layer",
		)
	}

	target, err := inspectProjectInfraTarget(projectRoot, foundry.path)
	if err != nil {
		return nil, err
	}
	hasEntrypoint := false
	if target.exists {
		hasEntrypoint, err = projectInfraHasEntrypoint(
			target.dir, infraProvider, foundry.module,
		)
		if err != nil {
			return nil, err
		}
	}
	if target.exists && !target.empty {
		return nil, projectInfraExistsError(
			foundry.path,
			"the Foundry layer already contains infrastructure",
		)
	}
	if target.exists && hasEntrypoint {
		return nil, projectInfraExistsError(
			foundry.path,
			"the Foundry layer already contains generated infrastructure",
		)
	}
	for i := range config.layers {
		layer := &config.layers[i]
		if layer != foundry &&
			sameProjectInfraPath(layer.path, foundry.path) {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				fmt.Sprintf(
					"infra layer %q already uses the Foundry layer path %q",
					layer.name, foundry.path,
				),
				"set the Foundry layer path to a unique project-relative directory",
			)
		}
	}
	return newProjectInfraEjectPlan(
		projectRoot, foundry.path, foundry.module, true,
		target.exists, nil,
	)
}

func projectRootInfraUserOwned(
	target projectInfraTarget,
	targetPath string,
	provider string,
	module string,
) (bool, error) {
	hasEntrypoint := false
	if target.exists {
		var err error
		hasEntrypoint, err = projectInfraHasEntrypoint(
			target.dir, provider, module,
		)
		if err != nil {
			return false, err
		}
	}
	if provider == provisioning.FoundryProviderName && hasEntrypoint {
		return false, projectInfraExistsError(
			targetPath,
			"the Foundry infrastructure already contains generated infrastructure",
		)
	}
	if provider == provisioning.TerraformProviderName {
		hasFoundryInfra, err := projectFoundryTerraformInfra(
			target.dir, module,
		)
		if err != nil {
			return false, err
		}
		if hasFoundryInfra {
			return false, projectInfraExistsError(
				targetPath,
				"the Foundry infrastructure already contains generated infrastructure",
			)
		}
	}
	builtIn := provider == provisioning.BicepProviderName ||
		provider == provisioning.TerraformProviderName
	if provider != "" && builtIn && !hasEntrypoint {
		return false, projectInfraProviderEntrypointError(
			provider, targetPath,
		)
	}
	custom := provider != "" && !builtIn &&
		provider != provisioning.FoundryProviderName
	if !hasEntrypoint && !custom && target.exists && !target.empty {
		return false, projectInfraMissingEntrypointError(targetPath)
	}
	return provider != provisioning.FoundryProviderName &&
		(hasEntrypoint || custom), nil
}

func sameProjectInfraPath(left, right string) bool {
	return strings.EqualFold(
		filepath.Clean(filepath.FromSlash(left)),
		filepath.Clean(filepath.FromSlash(right)),
	)
}

func newProjectInfraEjectPlan(
	projectRoot, targetPath, module string,
	layer bool,
	mergeExisting bool,
	updatedYAML []byte,
) (*projectInfraEjectPlan, error) {
	if module == "" {
		module = projectDefaultInfraModule
	}
	if module == "." || module == ".." ||
		filepath.Base(module) != module ||
		strings.ContainsAny(module, `/\`) {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf(
				"Foundry infrastructure module %q must be a file name",
				module,
			),
			"set infra.module or infra.layers[].module to a file name",
		)
	}
	if filepath.Ext(module) != "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf(
				"Foundry infrastructure module %q must not include a file extension",
				module,
			),
			"set infra.module or infra.layers[].module to a module base name",
		)
	}
	_, cleanPath, err := resolveProjectInfraPath(projectRoot, targetPath)
	if err != nil {
		return nil, err
	}
	target, err := inspectProjectInfraTarget(
		projectRoot, cleanPath,
	)
	if err != nil {
		return nil, err
	}
	if target.exists && !target.empty && !mergeExisting {
		return nil, projectInfraExistsError(
			filepath.ToSlash(cleanPath),
			"the target directory is not empty",
		)
	}
	return &projectInfraEjectPlan{
		targetDir:         target.dir,
		targetPath:        cleanPath,
		module:            module,
		layer:             layer,
		mergeExisting:     mergeExisting,
		updatedYAML:       updatedYAML,
		updateDescription: filepath.ToSlash(cleanPath),
	}, nil
}

func inspectProjectInfraTarget(
	projectRoot, targetPath string,
) (projectInfraTarget, error) {
	dir, cleanPath, err := resolveProjectInfraPath(projectRoot, targetPath)
	if err != nil {
		return projectInfraTarget{}, err
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return projectInfraTarget{dir: dir}, nil
	}
	if err != nil {
		return projectInfraTarget{}, fmt.Errorf(
			"check infrastructure path %s: %w", cleanPath, err,
		)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return projectInfraTarget{}, projectInfraExistsError(
			cleanPath, "the infrastructure path is a symbolic link",
		)
	}
	if !info.IsDir() {
		return projectInfraTarget{}, projectInfraExistsError(
			cleanPath, "the infrastructure path is not a directory",
		)
	}
	empty, err := projectInfraDirectoryEmpty(dir)
	if err != nil {
		return projectInfraTarget{}, fmt.Errorf(
			"inspect infrastructure path %s: %w", cleanPath, err,
		)
	}
	return projectInfraTarget{
		dir:    dir,
		exists: true,
		empty:  empty,
	}, nil
}

func projectInfraDirectoryEmpty(path string) (bool, error) {
	// #nosec G304
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	_, err = file.Readdirnames(1)
	switch {
	case errors.Is(err, io.EOF):
		return true, nil
	case err != nil:
		return false, err
	default:
		return false, nil
	}
}

func resolveProjectInfraPath(
	projectRoot, targetPath string,
) (string, string, error) {
	relativeTarget := filepath.FromSlash(targetPath)
	if filepath.IsAbs(relativeTarget) {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("infrastructure path %q is not relative", targetPath),
			"set infra.path to a project-relative directory",
		)
	}
	cleanPath := filepath.Clean(relativeTarget)
	if cleanPath == "." || cleanPath == ".." {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("infrastructure path %q is not a project directory", targetPath),
			"set infra.path to a project-relative directory below the project root",
		)
	}
	absolute, err := filepath.Abs(filepath.Join(projectRoot, cleanPath))
	if err != nil {
		return "", "", fmt.Errorf("resolve infrastructure path: %w", err)
	}
	relative, err := filepath.Rel(projectRoot, absolute)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("infrastructure path %q is outside the project", targetPath),
			"set infra.path to a project-relative directory",
		)
	}
	if err := ensureProjectInfraPathIsSafe(projectRoot, absolute); err != nil {
		return "", "", err
	}
	return absolute, filepath.ToSlash(relative), nil
}

func ensureProjectInfraPathIsSafe(projectRoot, target string) error {
	relative, err := filepath.Rel(projectRoot, target)
	if err != nil {
		return fmt.Errorf("resolve infrastructure path: %w", err)
	}
	current := projectRoot
	for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("inspect infrastructure path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return projectInfraExistsError(
				filepath.ToSlash(relative),
				"the infrastructure path contains a symbolic link",
			)
		}
	}
	return nil
}

func projectInfraExistsError(path, reason string) error {
	return exterrors.Validation(
		"infra_eject_exists",
		fmt.Sprintf("infrastructure path %q: %s", path, reason),
		"remove or rename the conflicting path and retry",
	)
}

func projectFoundryProvider(provider string) string {
	if provider == provisioning.TerraformProviderName {
		return provisioning.TerraformProviderName
	}
	return provisioning.FoundryProviderName
}

func projectInfraHasEntrypoint(
	dir, provider, module string,
) (bool, error) {
	if module == "" {
		module = projectDefaultInfraModule
	}
	switch provider {
	case provisioning.TerraformProviderName:
		return projectFoundryTerraformInfra(dir, module)
	case provisioning.FoundryProviderName, provisioning.BicepProviderName:
		return projectBicepHasEntrypoint(dir, module), nil
	default:
		info, err := os.Stat(dir)
		return err == nil && info.IsDir(), nil
	}
}

func projectBicepHasEntrypoint(dir, module string) bool {
	return fileExists(filepath.Join(dir, module+".bicep")) ||
		fileExists(filepath.Join(dir, module+".bicepparam"))
}

func projectInfraMissingEntrypointError(path string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidAzureYaml,
		fmt.Sprintf(
			"infrastructure path %q contains files but no detectable entry point",
			path,
		),
		"fix the infra configuration in azure.yaml and retry",
	)
}

func projectInfraProviderEntrypointError(provider, path string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidAzureYaml,
		fmt.Sprintf(
			"infrastructure declares provider %q but path %q contains no matching entry point",
			provider, path,
		),
		"fix the infra configuration in azure.yaml and retry",
	)
}

func projectFoundryTerraformInfra(dir, module string) (bool, error) {
	markerPath := filepath.Join(dir, projectTerraformMarker)
	markerInfo, err := os.Lstat(markerPath)
	if err == nil {
		if !markerInfo.Mode().IsRegular() {
			return false, invalidProjectTerraformMarker(
				markerPath, "marker is not a regular file",
			)
		}
		// #nosec G304
		marker, err := os.ReadFile(markerPath)
		if err != nil {
			return false, invalidProjectTerraformMarker(markerPath, err.Error())
		}
		if string(marker) != projectTerraformMarkerV1 {
			return false, invalidProjectTerraformMarker(
				markerPath, "marker version is unsupported or edited",
			)
		}
		return true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, invalidProjectTerraformMarker(markerPath, err.Error())
	}

	if !fileExists(filepath.Join(dir, module+".tfvars.json")) {
		return false, nil
	}
	// #nosec G304
	main, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		return false, nil
	}
	source := string(main)
	return strings.Contains(
		source, `resource "azapi_resource" "foundry_account"`,
	) && strings.Contains(
		source, `resource "azapi_resource" "project"`,
	) && strings.Contains(
		source, "Microsoft.CognitiveServices/accounts",
	) && strings.Contains(
		source, "Microsoft.CognitiveServices/accounts/projects",
	), nil
}

func invalidProjectTerraformMarker(path, reason string) error {
	return exterrors.Validation(
		exterrors.CodeInfraEjectMarkerInvalid,
		fmt.Sprintf(
			"Foundry ownership marker %q cannot be used: %s; eject did not modify the infrastructure",
			filepath.ToSlash(path), reason,
		),
		"restore the marker from source control or verify the infrastructure is user-owned",
	)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func installProjectInfraStage(
	stageDir string,
	plan *projectInfraEjectPlan,
) (func(), error) {
	if plan.mergeExisting {
		created, createdDirs, err := mergeProjectInfraStage(
			stageDir, plan.targetDir,
		)
		if err != nil {
			return nil, err
		}
		return func() {
			removeProjectInfraFiles(created)
			removeProjectInfraDirectories(createdDirs)
		}, nil
	}

	targetInfo, err := os.Lstat(plan.targetDir)
	if err == nil {
		if !targetInfo.IsDir() {
			return nil, projectInfraExistsError(
				plan.targetPath, "the target is not a directory",
			)
		}
		empty, readErr := projectInfraDirectoryEmpty(plan.targetDir)
		if readErr != nil {
			return nil, fmt.Errorf(
				"inspect infrastructure path %s: %w",
				plan.targetPath, readErr,
			)
		}
		if !empty {
			return nil, projectInfraExistsError(
				plan.targetPath, "the target directory is not empty",
			)
		}
		if err := os.Remove(plan.targetDir); err != nil {
			return nil, fmt.Errorf(
				"prepare infrastructure path %s: %w",
				plan.targetPath, err,
			)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf(
			"inspect infrastructure path %s: %w",
			plan.targetPath, err,
		)
	}

	// #nosec G301
	if err := os.MkdirAll(filepath.Dir(plan.targetDir), 0o755); err != nil {
		return nil, fmt.Errorf("create infrastructure path: %w", err)
	}
	if err := os.Rename(stageDir, plan.targetDir); err != nil {
		return nil, fmt.Errorf(
			"install infrastructure in %s: %w", plan.targetPath, err,
		)
	}
	return func() {
		// #nosec G703
		_ = os.RemoveAll(plan.targetDir)
		// #nosec G301 G703
		_ = os.MkdirAll(plan.targetDir, 0o755)
	}, nil
}

func mergeProjectInfraStage(
	stageDir, targetDir string,
) ([]string, []string, error) {
	var files []string
	err := filepath.WalkDir(stageDir, func(
		path string, entry fs.DirEntry, walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return projectInfraExistsError(
				filepath.ToSlash(path),
				"generated infrastructure contains a symbolic link",
			)
		}
		relative, err := filepath.Rel(stageDir, path)
		if err != nil || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return projectInfraExistsError(
				filepath.ToSlash(relative),
				"generated infrastructure path is invalid",
			)
		}
		destination := filepath.Join(targetDir, relative)
		if err := ensureProjectInfraPathIsSafe(
			targetDir, filepath.Dir(destination),
		); err != nil {
			return err
		}
		if _, err := os.Lstat(destination); err == nil {
			return projectInfraExistsError(
				filepath.ToSlash(relative),
				"generated infrastructure conflicts with an existing file",
			)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	var created []string
	var createdDirs []string
	for _, source := range files {
		relative, err := filepath.Rel(stageDir, source)
		if err != nil {
			removeProjectInfraFiles(created)
			removeProjectInfraDirectories(createdDirs)
			return nil, nil, err
		}
		destination := filepath.Join(targetDir, relative)
		if err := ensureProjectInfraPathIsSafe(
			targetDir, filepath.Dir(destination),
		); err != nil {
			removeProjectInfraFiles(created)
			removeProjectInfraDirectories(createdDirs)
			return nil, nil, err
		}
		directories, err := projectInfraDirectoriesToCreate(
			targetDir, filepath.Dir(destination),
		)
		if err != nil {
			removeProjectInfraFiles(created)
			removeProjectInfraDirectories(createdDirs)
			return nil, nil, err
		}
		createdDirs = append(createdDirs, directories...)
		// #nosec G301
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			removeProjectInfraFiles(created)
			removeProjectInfraDirectories(createdDirs)
			return nil, nil, fmt.Errorf(
				"create infrastructure directory: %w", err,
			)
		}
		if err := azdext.CopyFileAtomic(source, destination, 0o644); err != nil {
			removeProjectInfraFiles(created)
			removeProjectInfraDirectories(createdDirs)
			return nil, nil, fmt.Errorf(
				"install infrastructure file: %w", err,
			)
		}
		created = append(created, destination)
	}
	return created, createdDirs, nil
}

func removeProjectInfraFiles(files []string) {
	for i := len(files) - 1; i >= 0; i-- {
		_ = os.Remove(files[i])
	}
}

func removeProjectInfraDirectories(directories []string) {
	for i := len(directories) - 1; i >= 0; i-- {
		_ = os.Remove(directories[i])
	}
}

func projectInfraDirectoriesToCreate(
	root, target string,
) ([]string, error) {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return nil, fmt.Errorf("resolve infrastructure directory: %w", err)
	}
	current := root
	var directories []string
	for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			directories = append(directories, current)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect infrastructure directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, projectInfraExistsError(
				filepath.ToSlash(relative),
				"the infrastructure directory is not safe",
			)
		}
	}
	return directories, nil
}

func marshalProjectAzureYAML(document *yaml.Node) ([]byte, error) {
	data, err := yaml.Marshal(document)
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("marshal azure.yaml after infrastructure eject: %s", err),
		)
	}
	return data, nil
}

func yamlMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func yamlMappingScalar(mapping *yaml.Node, key string) string {
	value := yamlMappingValue(mapping, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(value.Value)
}

func yamlMappingNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func yamlScalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func yamlSetMappingScalar(mapping *yaml.Node, key, value string) {
	if existing := yamlMappingValue(mapping, key); existing != nil {
		existing.Value = value
		existing.Tag = "!!str"
		return
	}
	mapping.Content = append(mapping.Content, yamlScalarNode(key), yamlScalarNode(value))
}

func yamlRemoveMappingKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func newProjectInfraLayerNode(name, path, provider string) *yaml.Node {
	node := yamlMappingNode()
	yamlSetMappingScalar(node, "name", name)
	yamlSetMappingScalar(node, "path", filepath.ToSlash(path))
	yamlSetMappingScalar(node, "provider", provider)
	return node
}

func cloneProjectYAMLNode(value *yaml.Node) (*yaml.Node, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("clone infrastructure configuration: %w", err)
	}
	var clone yaml.Node
	if err := yaml.Unmarshal(data, &clone); err != nil || len(clone.Content) == 0 {
		if err != nil {
			return nil, fmt.Errorf("clone infrastructure configuration: %w", err)
		}
		return nil, fmt.Errorf("clone infrastructure configuration is empty")
	}
	return clone.Content[0], nil
}
