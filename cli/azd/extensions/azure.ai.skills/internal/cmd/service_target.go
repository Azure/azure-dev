// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// aiSkillHost is the azure.yaml service host kind owned by this extension. A
// `host: azure.ai.skill` service entry carries one Foundry skill, keyed by the
// skill name, and is reconciled (upserted) at deploy time by skillServiceTarget.
const aiSkillHost = "azure.ai.skill"

var _ azdext.ServiceTargetProvider = (*skillServiceTarget)(nil)

// skillServiceConfig is the service-level shape of a `host: azure.ai.skill`
// entry (see schemas/azure.ai.skill.json). The skill name is the azure.yaml
// service key, not a body field.
type skillServiceConfig struct {
	Description  string   `json:"description,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	Archive      string   `json:"archive,omitempty"`
}

// skillServiceTarget upserts a Foundry skill declared as an azure.ai.skill
// service. Deploy creates a new default skill version from either inline
// instructions or an archive reference; the resource name is the service key.
// Package and Publish are no-ops because a skill has no build artifact.
type skillServiceTarget struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
}

// newSkillServiceTarget creates the azure.ai.skill service-target provider.
func newSkillServiceTarget(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &skillServiceTarget{azdClient: azdClient}
}

// Initialize stores the service configuration; no other setup is required.
func (p *skillServiceTarget) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints returns no endpoints; a skill service exposes none.
func (p *skillServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

// GetTargetResource delegates to azd's default resolver and falls back to a
// minimal target so the deploy pipeline can proceed; the skill upsert targets
// the Foundry project endpoint, not an ARM resource.
func (p *skillServiceTarget) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	if defaultResolver != nil {
		if target, err := defaultResolver(); err == nil && target != nil {
			return target, nil
		}
	}
	return &azdext.TargetResource{SubscriptionId: subscriptionId}, nil
}

// Package is a no-op; a skill has nothing to build or stage.
func (p *skillServiceTarget) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{}, nil
}

// Publish is a no-op; a skill has no artifact to publish.
func (p *skillServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy upserts the skill by creating a new default version from the entry's
// inline instructions or archive reference. Re-running deploy creates another
// immutable version rather than failing. Removing the service from azure.yaml
// stops azd managing the skill but does not delete it (use `azd ai skill delete`).
func (p *skillServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	cfg, err := parseSkillServiceConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	hasInline := cfg.Description != "" || cfg.Instructions != "" || len(cfg.Tools) > 0
	if cfg.Archive != "" && hasInline {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf(
				"skill service %q cannot combine archive with description, instructions, or tools",
				serviceConfig.GetName(),
			),
			"configure either archive, or inline description/instructions/tools, but not both",
		)
	}
	if cfg.Archive == "" && cfg.Instructions == "" {
		return nil, exterrors.Validation(
			exterrors.CodeMissingRequiredField,
			fmt.Sprintf("skill service %q requires instructions or archive", serviceConfig.GetName()),
			"set instructions to inline text/a .md or .txt path, or set archive to a .zip/directory path",
		)
	}

	if progress != nil {
		progress(fmt.Sprintf("Upserting skill %q", serviceConfig.GetName()))
	}

	skillCtx, err := resolveSkillContext(ctx, "")
	if err != nil {
		return nil, err
	}

	if cfg.Archive != "" {
		projectResp, err := p.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return nil, fmt.Errorf("resolving azd project path for skill archive: %w", err)
		}
		archivePath, err := resolveSkillArchivePath(projectResp.GetProject().GetPath(), serviceConfig, cfg.Archive)
		if err != nil {
			return nil, err
		}
		archive, err := prepareSkillArchive(archivePath)
		if err != nil {
			return nil, err
		}
		defer archive.Reader.Close()

		if _, err := skillCtx.client.CreateVersionFromZip(
			ctx,
			serviceConfig.GetName(),
			archive.Name,
			archive.Reader,
			true,
		); err != nil {
			return nil, exterrors.ServiceFromAzure(err, exterrors.OpReconcileSkill)
		}
	} else {
		instructions, err := resolveSkillInstructions(serviceConfig, cfg.Instructions)
		if err != nil {
			return nil, err
		}
		if _, err := skillCtx.client.CreateVersionInline(
			ctx,
			serviceConfig.GetName(),
			skill_api.CreateVersionRequest{
				InlineContent: &skill_api.SkillInlineContent{
					Description:  cfg.Description,
					Instructions: instructions,
					AllowedTools: cfg.Tools,
				},
				Default: true,
			},
		); err != nil {
			return nil, exterrors.ServiceFromAzure(err, exterrors.OpReconcileSkill)
		}
	}

	return &azdext.ServiceDeployResult{}, nil
}

// parseSkillServiceConfig reads the service-level (inline) skill properties,
// falling back to the deprecated config: shape for azure.yaml files written
// before the per-resource service split.
func parseSkillServiceConfig(svc *azdext.ServiceConfig) (*skillServiceConfig, error) {
	props := svc.GetAdditionalProperties()
	if props == nil || len(props.GetFields()) == 0 {
		props = svc.GetConfig()
	}
	cfg := &skillServiceConfig{}
	if props == nil {
		return cfg, nil
	}
	b, err := json.Marshal(props.AsMap())
	if err != nil {
		return nil, fmt.Errorf("encoding skill service %q config: %w", svc.GetName(), err)
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parsing skill service %q config: %w", svc.GetName(), err)
	}
	return cfg, nil
}

func resolveSkillInstructions(svc *azdext.ServiceConfig, instructions string) (string, error) {
	if !isInstructionFilePath(instructions) {
		return instructions, nil
	}

	path := strings.TrimSpace(instructions)
	if !filepath.IsAbs(path) {
		// Reject path traversal: a relative instructions path is read from disk
		// under the service directory, so a value like "../../secret.md" must not
		// be allowed to escape it via filepath.Join.
		if hasParentTraversal(path) {
			return "", fmt.Errorf(
				"skill instructions path %q must not contain '..' or escape the service directory", instructions)
		}
		baseDir := svc.GetRelativePath()
		if baseDir == "" {
			baseDir = "."
		}
		path = filepath.Join(baseDir, path)
	}

	data, err := readFileWithLimit(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type preparedSkillArchive struct {
	Name   string
	Reader io.ReadCloser
}

func resolveSkillArchivePath(projectPath string, svc *azdext.ServiceConfig, archive string) (string, error) {
	path := strings.TrimSpace(archive)
	if filepath.IsAbs(path) {
		return path, nil
	}
	if hasParentTraversal(path) {
		return "", exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("skill archive path %q must not contain '..' or escape the service directory", archive),
			"move the archive inside the service directory and use a relative path without '..'",
		)
	}
	baseDir := svc.GetRelativePath()
	if baseDir == "" {
		baseDir = "."
	}
	return filepath.Join(projectPath, baseDir, path), nil
}

func prepareSkillArchive(path string) (*preparedSkillArchive, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot inspect skill archive %s: %s", path, err),
			"verify the archive or directory exists and is readable",
		)
	}

	if info.IsDir() {
		if _, found, err := skill_api.LocateSkillMdInDir(path); err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidSkillFile,
				fmt.Sprintf("cannot inspect SKILL.md in %s: %s", path, err),
				"verify the directory is readable and SKILL.md is a regular file",
			)
		} else if !found {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidSkillFile,
				fmt.Sprintf("skill archive directory %s does not contain SKILL.md at its root", path),
				"add SKILL.md to the directory root or reference a .zip archive",
			)
		}

		data, err := skill_api.ArchiveDirectory(path, skill_api.ArchiveOptions{})
		if err != nil {
			return nil, classifyArchiveDirectoryError(err, path)
		}
		name := filepath.Base(filepath.Clean(path)) + ".zip"
		return &preparedSkillArchive{
			Name:   name,
			Reader: io.NopCloser(bytes.NewReader(data)),
		}, nil
	}

	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("skill archive %s must be a .zip file or a directory containing SKILL.md", path),
			"set archive to a .zip file or a directory containing SKILL.md",
		)
	}
	file, err := os.Open(path) //nolint:gosec // user-authored azure.yaml path opened on user's behalf
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot open skill archive %s: %s", path, err),
			"verify the archive is readable",
		)
	}
	return &preparedSkillArchive{
		Name:   filepath.Base(path),
		Reader: file,
	}, nil
}

// hasParentTraversal reports whether a relative path contains a ".." segment
// that could escape its base directory, treating both '/' and '\' as separators.
func hasParentTraversal(p string) bool {
	for seg := range strings.SplitSeq(strings.ReplaceAll(p, "\\", "/"), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func isInstructionFilePath(instructions string) bool {
	value := strings.TrimSpace(instructions)
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	switch strings.ToLower(filepath.Ext(value)) {
	case ".md", ".txt":
		return true
	default:
		return false
	}
}
