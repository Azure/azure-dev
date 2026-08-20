// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
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
	EnsureEval(ctx context.Context, group Eval, datasetPath string) (id string, created bool, err error)
	// ReserveDeclared marks the evals these declarations already resolve to as
	// spoken for, so no other declaration adopts one. Called once before
	// reconciling, because adoption otherwise depends on the order the file
	// lists them in.
	ReserveDeclared(ctx context.Context, groups []Eval)
}

// EvalServiceTargetProvider deploys eval resources during `azd up`. azd owns
// ordering across services through `uses:`; this provider owns only the order
// within the eval service itself.
type EvalServiceTargetProvider struct {
	azdClient     *azdext.AzdClient
	newReconciler func(ctx context.Context) (Reconciler, error)

	serviceConfig *azdext.ServiceConfig
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
		return nil, messages.EvalConfigInvalid(err)
	}

	reconciler, err := p.newReconciler(ctx)
	if err != nil {
		return nil, err
	}

	baseDir := p.evalBaseDir(ctx, serviceConfig)

	// 1. Datasets the configuration owns. Paths are kept so an eval that names
	// one can derive its columns without reading the blob back.
	//
	// A declaration with no `file:` is included rather than skipped: it names
	// a dataset that is already registered, and reconciling it is what confirms
	// it is really there and settles which version a `version:` pin selected.
	// Skipping it would leave a misspelled name to surface as a failed run.
	datasetPaths := map[string]string{}
	for _, decl := range cfg.Datasets {
		report(progress, messages.ReconcilingDataset(decl.Name))
		localPath := ResolveSource(baseDir, decl.File)
		datasetPaths[decl.Name] = localPath
		version, changed, err := reconciler.EnsureDataset(ctx, decl, localPath)
		if err != nil {
			return nil, messages.DatasetProblem(decl.Name, err)
		}
		report(progress, describeResult("dataset", decl.Name, version, changed))
	}

	// 2. Evaluators this configuration owns. Built-ins and already-registered
	// ones need no publish.
	for _, decl := range cfg.CustomEvaluators() {
		report(progress, messages.ReconcilingEvaluator(decl.Name))
		// A rubric written out in the configuration has no file to read.
		localPath := ""
		if decl.Source != "" {
			localPath = ResolveSource(baseDir, decl.Source)
		}
		version, changed, err := reconciler.EnsureEvaluator(ctx, decl, localPath)
		if err != nil {
			return nil, messages.EvaluatorProblem(decl.Name, err)
		}
		report(progress, describeResult("evaluator", decl.Name, version, changed))
	}

	// 3. The evals. An eval is recreated only when its own declaration changed:
	// the comparison covers what the entry declares, not what its references
	// resolve to. An evaluator tracking latest that publishes a new version
	// leaves every eval that runs it alone, which is what keeps a rubric edit
	// comparable against the runs before it.
	reconciler.ReserveDeclared(ctx, cfg.Evals)

	for i := range cfg.Evals {
		eval := cfg.Evals[i]
		report(progress, messages.ReconcilingEval(eval.Name))
		id, created, err := reconciler.EnsureEval(ctx, eval, datasetPaths[eval.Dataset])
		if err != nil {
			return nil, messages.EvalProblem(eval.Name, err)
		}
		report(progress, describeEval(eval.Name, id, created))
	}

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

// evalBaseDir is the directory a declaration's `source:` resolves against.
//
// serviceRelativeDir answers relative to the project, because that is what the
// service's `$ref` and relativePath are written relative to. Left there it was
// resolved against this process's working directory instead, and azd neither
// changes it nor reports the project through it: azure.yaml is found by walking
// up from wherever the caller stood, and AZD_CWD carries the --cwd flag and
// nothing else. So `azd up` from any subdirectory of the project reported every
// dataset as not yet generated, and the remedy it offered would have billed a
// generation job to rewrite a file already on disk.
//
// The same join is what agent_instructions.go does with the same helper.
func (p *EvalServiceTargetProvider) evalBaseDir(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) string {
	return baseDirUnder(p.projectRoot(ctx), serviceConfig)
}

// baseDirUnder places a service's directory under the project.
//
// azd does not re-root an absolute `$ref` or an absolute `project:`, so neither
// does this: joining one under the project produced <root>/C:/shared/evals,
// which is the bug this fixes, reached from a different input. An empty root is
// azd having failed to name the project, where the relative path is what the
// extension did before and is better than resolving against nothing.
func baseDirUnder(projectRoot string, serviceConfig *azdext.ServiceConfig) string {
	relative := serviceRelativeDir(serviceConfig)
	if filepath.IsAbs(relative) || projectRoot == "" {
		return relative
	}
	return filepath.Join(projectRoot, relative)
}

// describeResult reports whether a version was published or reused, so a
// no-op deploy is visibly a no-op.
func describeResult(kind, name, version string, changed bool) string {
	if changed {
		return messages.PublishedVersion(kind, name, version)
	}
	return messages.UnchangedAtVersion(kind, name, version)
}

// describeEval keeps a deploy's eval line saying the same thing the direct
// command says. Reporting the id either way left a deploy unable to answer
// whether it published anything.
func describeEval(name, id string, created bool) string {
	if created {
		return messages.EvalCreatedProgress(name, id)
	}
	return messages.EvalUnchangedProgress(name, id)
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
		return nil, messages.ServiceCarriesNoConfig(svc.GetName())
	}

	values := props.AsMap()
	if projectRoot != "" {
		resolved, err := resolveEvalRefs(values, projectRoot)
		if err != nil {
			return nil, err
		}
		values = resolved
	} else if containsRefDirective(values) {
		// Without a project root there is nothing to resolve paths against, and
		// deleting the directive below would deploy a silently truncated
		// configuration: an entry that mixes inline content with an include
		// loses only the included half, and the failure then names a missing
		// eval rather than the include nobody could read.
		return nil, messages.RefNeedsAProjectRoot(svc.GetName())
	}

	// `$ref` is a directive, not configuration: ResolveFileRefs has already
	// replaced it with the file's content. It only survives when resolution was
	// skipped, and that config cannot deploy anyway.
	delete(values, "$ref")

	// Decoded by the same strict reader the on-disk path uses, so `azd up` and
	// `azd ai eval run` name a mistyped key identically instead of one
	// explaining it and the other ignoring it.
	raw, err := yaml.Marshal(values)
	if err != nil {
		return nil, messages.ReadingServiceConfig(err)
	}
	cfg, err := DecodeEvalConfig(raw, svc.GetName())
	if err != nil {
		return nil, err
	}
	return cfg, nil
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

// ResolveSource joins a declared source against the directory holding the
// configuration, leaving absolute paths and empty values alone.
//
// Exported because `eval create` resolves the same declarations as `azd up`
// and had grown its own copy that joined unconditionally, so an absolute
// source came out as evals/C:/data/rows.jsonl there while `azd up` handled it.
// One resolver is what stops the two drifting again.
func ResolveSource(baseDir, source string) string {
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
		return "", messages.Hashing(path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// FingerprintBytes hashes content that was never a file, which is how a rubric
// written in the configuration gets the change detection a rubric file has.
func FingerprintBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// FingerprintGroup hashes an eval's own declaration.
//
// Change detection on upstream artifacts is not sufficient: editing a group's
// evaluators, target, or options changes what the group means, and groups are
// immutable, so the group has to be recreated even when the dataset and
// evaluators are untouched. Without this a retargeted group keeps running
// against the old definition.
func FingerprintGroup(group Eval) (string, error) {
	// Only substance is hashed. The id is server-assigned; name and description
	// are what UpdateEvalParametersBody reaches, so an edit confined to them is
	// pushed in place and must not cost the eval its id and its run history.
	// Everything else — dataset, source, evaluators, target, level — is what
	// makes this declaration the one it is.
	name := group.Name
	group.ID = ""
	group.Name = ""
	group.Description = ""

	data, err := json.Marshal(group)
	if err != nil {
		return "", messages.HashingEval(name, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// FingerprintDefinition hashes only what the service stores.
//
// max_samples and source: are applied per run, not at creation --
// CreateOpenAIEvalRequest carries neither and buildEvalRequest reads neither.
// Recreating the eval when one of them changes points the declaration at a new
// id and leaves every run taken before it reachable only through the old one,
// for an edit the stored eval cannot even express.
//
// Kept separate from FingerprintGroup rather than folded into it, because that
// digest also answers "which eval was this declaration before it was renamed".
// Two evals over the same dataset and evaluators that differ only in their
// window are two evals, and one key for both hands the second the first one's
// id -- so the second is never created, and the first is renamed to whichever
// declaration came last.
//
// The cost is that renaming an eval and changing its window in one edit is read
// as a new eval rather than a rename. That forks a history, which is the
// conservative direction: the other way silently merges two.
func FingerprintDefinition(group Eval) (string, error) {
	group.MaxSamples = 0
	group.Source = nil
	return FingerprintGroup(group)
}

// FingerprintKey is the azd environment key holding an artifact's fingerprint.
//
// The readable half is lossy: everything outside [A-Z0-9] becomes an
// underscore, so `quality-a`, `quality_a` and `quality a` all sanitize alike,
// as does any pair of names differing only outside ASCII. Two artifacts sharing
// a key overwrite each other's recorded fingerprint, version and id, which
// makes every deploy republish both. The trailing digest keeps them apart.
func FingerprintKey(kind, name string) string {
	readable := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r >= 'a' && r <= 'z':
			return r - 32
		default:
			return '_'
		}
	}, kind+"_"+name)

	sum := sha256.Sum256([]byte(kind + "\x00" + name))
	return EnvKeyFingerprintPrefix + readable + "_" + strings.ToUpper(hex.EncodeToString(sum[:4]))
}
