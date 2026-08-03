// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/protobuf/types/known/structpb"
)

// EvalHost is the azure.yaml host this provider serves.
const EvalHost = "azure.ai.eval"

// azd environment keys owned by this extension.
const (
	EnvKeyEvalID            = "EVAL_ID"
	EnvKeyDatasetVersion    = "EVAL_DATASET_VERSION"
	EnvKeyFingerprintPrefix = "EVAL_FINGERPRINT_"
)

// Reconciler applies the eval configuration to the service. It is satisfied by
// the command layer, which owns the data-plane clients.
type Reconciler interface {
	// EnsureDataset registers a new dataset version when the local content
	// changed, returning the resolved version and whether anything was written.
	EnsureDataset(ctx context.Context, decl DatasetDecl, localPath string) (version string, changed bool, err error)
	// EnsureEvaluator registers a new evaluator version when the definition
	// differs from what the service already holds.
	EnsureEvaluator(ctx context.Context, decl EvaluatorDecl, localPath string) (version string, changed bool, err error)
	// EnsureEval creates the group when it is absent or its resolved
	// evaluators or options changed, returning its id. datasetPath is the local
	// dataset backing the group, or empty when it is already registered; it lets
	// the reconciler bind criteria to the columns that actually exist.
	EnsureEval(ctx context.Context, group Eval, datasetPath string, recreate bool) (id string, err error)
}

// EvalServiceTargetProvider deploys eval resources during `azd up`. azd owns
// ordering across services through `uses:`; this provider owns only the order
// within the eval service itself.
type EvalServiceTargetProvider struct {
	azdClient     *azdext.AzdClient
	newReconciler func(ctx context.Context) (Reconciler, error)

	serviceConfig *azdext.ServiceConfig
	envName       string
}

// NewEvalServiceTargetProvider builds the provider. The reconciler is supplied
// lazily so the data-plane clients are only created when a deploy actually runs.
func NewEvalServiceTargetProvider(
	azdClient *azdext.AzdClient,
	newReconciler func(ctx context.Context) (Reconciler, error),
) *EvalServiceTargetProvider {
	return &EvalServiceTargetProvider{azdClient: azdClient, newReconciler: newReconciler}
}

func (p *EvalServiceTargetProvider) Initialize(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) error {
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints reports no endpoints: eval resources are not addressable.
func (p *EvalServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

func (p *EvalServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	if defaultResolver != nil {
		if target, err := defaultResolver(); err == nil {
			return target, nil
		}
	}
	// Eval resources live on the project data plane, so there is no ARM
	// resource of our own to resolve.
	return &azdext.TargetResource{SubscriptionId: subscriptionId}, nil
}

// Package is a no-op: eval artifacts are plain files already on disk.
func (p *EvalServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{}, nil
}

// Publish is a no-op: there is no artifact registry step for eval resources.
func (p *EvalServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy reconciles the eval configuration in a fixed order — datasets, then
// evaluators, then evals — because a group references the versions the
// first two resolve to. It fails fast; the next `azd up` resumes from wherever
// it stopped.
func (p *EvalServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	cfg, err := EvalConfigFromService(serviceConfig, p.projectRoot(ctx))
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("eval config is invalid: %w", err)
	}

	reconciler, err := p.newReconciler(ctx)
	if err != nil {
		return nil, err
	}

	baseDir := serviceRelativeDir(serviceConfig)

	// The eval takes its name from the service entry that pulled this config
	// in, which is what makes one service per eval work.
	eval := cfg.Eval(serviceConfig.Name)

	// 1. Dataset.
	anyChanged := false
	datasetPath := ""
	if cfg.Dataset != nil {
		report(progress, fmt.Sprintf("Reconciling dataset %s", cfg.Dataset.Name))
		datasetPath = resolveSource(baseDir, cfg.Dataset.Source)
		version, changed, err := reconciler.EnsureDataset(ctx, *cfg.Dataset, datasetPath)
		if err != nil {
			return nil, fmt.Errorf("dataset %q: %w", cfg.Dataset.Name, err)
		}
		anyChanged = anyChanged || changed
		report(progress, describeResult("dataset", cfg.Dataset.Name, version, changed))
	}

	// 2. Evaluators this config owns. Built-ins and already-registered ones
	// need no publish.
	for _, decl := range cfg.CustomEvaluators() {
		report(progress, fmt.Sprintf("Reconciling evaluator %s", decl.Name))
		localPath := resolveSource(baseDir, decl.Source)
		version, changed, err := reconciler.EnsureEvaluator(ctx, decl, localPath)
		if err != nil {
			return nil, fmt.Errorf("evaluator %q: %w", decl.Name, err)
		}
		anyChanged = anyChanged || changed
		report(progress, describeResult("evaluator", decl.Name, version, changed))
	}

	// 3. The eval. Evals are immutable, so a change upstream means a new one
	// must be created and the stored id replaced.
	report(progress, fmt.Sprintf("Reconciling eval %s", eval.Name))
	id, err := reconciler.EnsureEval(ctx, eval, datasetPath, anyChanged)
	if err != nil {
		return nil, fmt.Errorf("eval %q: %w", eval.Name, err)
	}
	report(progress, fmt.Sprintf("Eval %s is %s", eval.Name, id))

	return &azdext.ServiceDeployResult{}, nil
}

// projectRoot is the directory `$ref` paths resolve against. It is the
// directory holding azure.yaml, which only azd can report.
func (p *EvalServiceTargetProvider) projectRoot(ctx context.Context) string {
	if p.azdClient == nil {
		return ""
	}
	resp, err := p.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return ""
	}
	return resp.GetProject().GetPath()
}

// describeResult reports whether a version was published or reused, so a
// no-op deploy is visibly a no-op.
func describeResult(kind, name, version string, changed bool) string {
	if changed {
		return fmt.Sprintf("Published %s %s version %s", kind, name, version)
	}
	return fmt.Sprintf("%s %s is unchanged at version %s", strings.ToUpper(kind[:1])+kind[1:], name, version)
}

func report(progress azdext.ProgressReporter, message string) {
	if progress != nil {
		progress(message)
	}
}

// EvalConfigFromService reads the eval configuration carried inline on the
// service entry. azd captures unknown keys into AdditionalProperties and hands
// them to the extension untouched.
//
// azd core deliberately does not resolve `$ref` includes for extensions — it
// strips the ServiceConfig fields it owns and leaves `$ref` at the top of the
// map for the owning extension to resolve. Without this call a service written
// as `host: azure.ai.eval` + `$ref: ./evals/azure.yaml` deploys nothing at all,
// because the config parses to an empty set of datasets and groups.
func EvalConfigFromService(svc *azdext.ServiceConfig, projectRoot string) (*EvalConfig, error) {
	props := serviceProps(svc)
	if props == nil || len(props.GetFields()) == 0 {
		return nil, fmt.Errorf(
			"service %q carries no eval configuration; expected evaluators, datasets, or evals",
			svc.GetName())
	}

	values := props.AsMap()
	if projectRoot != "" {
		resolved, err := foundry.ResolveFileRefs(values, projectRoot)
		if err != nil {
			return nil, fmt.Errorf("resolving $ref in the eval service configuration: %w", err)
		}
		values = resolved
	}

	raw, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("reading the eval service configuration: %w", err)
	}

	var cfg EvalConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parsing the eval service configuration: %w", err)
	}
	return &cfg, nil
}

// serviceProps prefers the inline properties, falling back to the nested
// config block.
func serviceProps(svc *azdext.ServiceConfig) *structpb.Struct {
	if s := svc.GetAdditionalProperties(); s != nil && len(s.GetFields()) > 0 {
		return s
	}
	return svc.GetConfig()
}

// serviceRelativeDir returns the directory that `source:` paths resolve against.
//
// When the service is authored as `host:` + `$ref: ./evals/azure.yaml`, the
// paths inside that file are written relative to the file itself, so the
// include's own directory is the base. ResolveFileRefs inlines the content
// without rebasing paths, so the base has to be recovered from the `$ref`
// value before resolution.
func serviceRelativeDir(svc *azdext.ServiceConfig) string {
	if svc == nil {
		return "."
	}
	if props := serviceProps(svc); props != nil {
		if ref, ok := props.AsMap()["$ref"].(string); ok && ref != "" {
			if dir := filepath.Dir(filepath.FromSlash(ref)); dir != "" {
				return dir
			}
		}
	}
	if p := svc.GetRelativePath(); p != "" {
		return p
	}
	return "."
}

// resolveSource joins a declared source against the service directory, leaving
// absolute paths and empty values alone.
func resolveSource(baseDir, source string) string {
	if source == "" {
		return ""
	}
	if filepath.IsAbs(source) {
		return source
	}
	return filepath.Join(baseDir, source)
}

// Fingerprint hashes a local artifact so a later deploy can tell whether the
// content changed without downloading anything from the service.
//
// The dataset API returns no content hash or etag, so comparing against the
// service would mean downloading the blob on every deploy. Every artifact this
// applies to — a dataset, a rubric, an evaluator script — is a single file.
func Fingerprint(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hashing %q: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// FingerprintGroup hashes an eval's own declaration.
//
// Change detection on upstream artifacts is not sufficient: editing a group's
// evaluators, target, or options changes what the group means, and groups are
// immutable, so the group has to be recreated even when the dataset and
// evaluators are untouched. Without this a retargeted group keeps running
// against the old definition.
func FingerprintGroup(group Eval) (string, error) {
	// The id is server-assigned. The description is carried in the group's
	// metadata, so editing it does change the request, but recreating an
	// immutable group over a reworded description would cost the group id and
	// break comparison against earlier runs. It is documentation, not
	// evaluation semantics, so an edit lands the next time the group is
	// recreated for a reason that matters.
	group.ID = ""
	group.Description = ""

	data, err := json.Marshal(group)
	if err != nil {
		return "", fmt.Errorf("hashing eval %q: %w", group.Name, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// FingerprintKey is the azd environment key holding an artifact's fingerprint.
func FingerprintKey(kind, name string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r >= 'a' && r <= 'z':
			return r - 32
		default:
			return '_'
		}
	}, kind+"_"+name)
	return EnvKeyFingerprintPrefix + safe
}
