// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/project"
)

// evalReconciler applies the eval configuration to the data plane. It is the
// deploy half of the provider; the provider owns ordering, this owns the calls.
type evalReconciler struct {
	ec *evalContext
}

var _ project.Reconciler = (*evalReconciler)(nil)

func newEvalReconciler(ctx context.Context) (project.Reconciler, error) {
	ec, err := newEvalContext(ctx, "")
	if err != nil {
		return nil, err
	}
	return &evalReconciler{ec: ec}, nil
}

// EnsureDataset registers a new version only when the local content changed.
//
// The dataset API exposes no content hash, so comparing against the service
// would mean downloading the blob on every deploy. Instead the local file is
// hashed and the digest kept in the azd environment.
func (r *evalReconciler) EnsureDataset(
	ctx context.Context,
	decl project.DatasetDecl,
	localPath string,
) (string, bool, error) {
	// No local source means the dataset is already registered; just confirm it.
	if localPath == "" {
		version := decl.Version
		if version == "" {
			list, err := r.ec.datasetClient.ListDatasetVersions(
				ctx, decl.Name, ProjectEndpointAPIVersion,
			)
			if err != nil {
				return "", false, fmt.Errorf(
					"dataset %q has no local source and could not be found on the project: %w",
					decl.Name, err)
			}
			if len(list.Value) == 0 {
				return "", false, fmt.Errorf(
					"dataset %q has no local source and is not registered on the project", decl.Name)
			}
			version = dataset_api.LatestVersion(list.Value)
		}
		return version, false, nil
	}

	if _, err := os.Stat(localPath); err != nil {
		return "", false, fmt.Errorf("dataset source %q: %w", localPath, err)
	}

	digest, err := project.Fingerprint(localPath)
	if err != nil {
		return "", false, err
	}

	key := project.FingerprintKey("dataset", decl.Name)
	if prior := r.ec.getEnvValue(ctx, key); prior == digest {
		// Unchanged since the last deploy; reuse the recorded version, but only
		// after confirming nobody published a newer one outside the repo. An
		// explicit `version:` is the author saying which version they want, so
		// it settles the question and the check does not apply.
		if version := r.ec.getEnvValue(ctx, versionKey("dataset", decl.Name)); version != "" {
			if decl.Version != "" {
				return decl.Version, false, nil
			}
			if err := r.checkDatasetDrift(ctx, decl.Name, version); err != nil {
				return "", false, err
			}
			return version, false, nil
		}
	}

	// The upload helper scans a directory for the first .jsonl.
	dir := localPath
	if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
		dir = filepath.Dir(localPath)
	}

	// UploadNextVersion discovers the currently registered version when none is
	// declared, so the upload does not restart at 1.0 and collide.
	ds, err := r.ec.datasetClient.UploadNextVersion(
		ctx, decl.Name, decl.Version, dir, ProjectEndpointAPIVersion,
	)
	if err != nil {
		return "", false, err
	}

	_ = r.ec.setEnvValue(ctx, key, digest)
	_ = r.ec.setEnvValue(ctx, versionKey("dataset", decl.Name), ds.Version)
	_ = r.ec.setEnvValue(ctx, envKeyDatasetVersion, ds.Version)

	return ds.Version, true, nil
}

// checkDatasetDrift fails when the service holds a newer version than the one
// recorded at the last deploy.
//
// Local content being unchanged is not enough to reuse the recorded version:
// someone may have published a newer one outside the repo, and silently
// pinning the eval group to the older version would quietly evaluate against
// stale data. Publishing is not destructive — versions are immutable — so the
// remedy is to sync, not to overwrite.
func (r *evalReconciler) checkDatasetDrift(
	ctx context.Context,
	name, recorded string,
) error {
	latest := r.latestDatasetVersion(ctx, name)
	if latest == "" || latest == recorded {
		return nil
	}
	if !dataset_api.VersionGreater(latest, recorded) {
		return nil
	}
	return fmt.Errorf(
		"dataset %q is at version %s on the project but %s was recorded at the last deploy; "+
			"someone published a version outside this repo. "+
			"Pin it with `version: %s` on the dataset, or pull the newer content locally, "+
			"then deploy again",
		name, latest, recorded, latest)
}

// latestDatasetVersion reports the newest registered version, or empty when the
// dataset is unknown or the listing has not caught up.
func (r *evalReconciler) latestDatasetVersion(ctx context.Context, name string) string {
	list, err := r.ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
	if err != nil || list == nil || len(list.Value) == 0 {
		return ""
	}
	return dataset_api.LatestVersion(list.Value)
}

// EnsureEvaluator publishes a new version when the local definition differs
// from what the service holds. Evaluator definitions come back inline, so this
// compares content directly rather than relying on a cached digest.
func (r *evalReconciler) EnsureEvaluator(
	ctx context.Context,
	decl project.EvaluatorDecl,
	localPath string,
) (string, bool, error) {
	if localPath == "" {
		raw, err := r.ec.evalClient.GetEvaluatorRaw(
			ctx, decl.Name, decl.Version, ProjectEndpointAPIVersion,
		)
		if err != nil {
			return "", false, fmt.Errorf(
				"evaluator %q has no local source and could not be found on the project: %w",
				decl.Name, err)
		}
		return versionFromRaw(raw, decl.Version), false, nil
	}

	raw, err := os.ReadFile(localPath)
	if err != nil {
		return "", false, fmt.Errorf("evaluator source %q: %w", localPath, err)
	}

	body, err := normalizeRubricBody(decl.Name, raw)
	if err != nil {
		return "", false, fmt.Errorf("evaluator %q: %w", decl.Name, err)
	}

	// Compare against the definition already on the service.
	if existing, err := r.ec.evalClient.GetEvaluatorRaw(
		ctx, decl.Name, "", ProjectEndpointAPIVersion,
	); err == nil {
		if sameDefinition(existing, body) {
			return versionFromRaw(existing, decl.Version), false, nil
		}
	}

	created, err := r.ec.evalClient.CreateEvaluatorVersion(
		ctx, decl.Name, body, ProjectEndpointAPIVersion,
	)
	if err != nil {
		return "", false, err
	}
	return created.Version, true, nil
}

// EnsureEvalGroup creates the group when it has never been deployed, or when an
// upstream artifact changed. Groups are immutable, so a change means a new
// group and a new id.
func (r *evalReconciler) EnsureEvalGroup(
	ctx context.Context,
	group project.EvalGroup,
	datasetPath string,
	recreate bool,
) (string, error) {
	if group.ID != "" {
		return group.ID, nil
	}

	// Groups are immutable, so a change to the group's own declaration —
	// evaluators, target, or options — needs a new group just as much as a
	// change to an upstream artifact does.
	digest, err := project.FingerprintGroup(group)
	if err != nil {
		return "", err
	}
	key := project.FingerprintKey("evalgroup", group.Name)
	if prior := r.ec.getEnvValue(ctx, key); prior != "" && prior != digest {
		recreate = true
	}

	cached := r.ec.getEnvValue(ctx, idKey("evalgroup", group.Name))
	if cached != "" && !recreate {
		if _, err := r.ec.evalClient.GetOpenAIEval(ctx, cached); err == nil {
			// Record the digest on reuse as well, otherwise a group deployed
			// before fingerprinting existed never establishes a baseline and
			// later edits go undetected.
			_ = r.ec.setEnvValue(ctx, key, digest)
			_ = r.ec.setEnvValue(ctx, envKeyEvalGroupID, cached)
			return cached, nil
		}
	}

	req, err := buildEvalGroupRequest(
		&group,
		r.ec.evaluatorSchemas(ctx),
		datasetColumnsFromPath(datasetPath),
	)
	if err != nil {
		return "", err
	}
	created, err := r.ec.evalClient.CreateOpenAIEval(ctx, req)
	if err != nil {
		return "", err
	}
	_ = r.ec.setEnvValue(ctx, key, digest)
	_ = r.ec.setEnvValue(ctx, idKey("evalgroup", group.Name), created.ID)
	// EVAL_GROUP_ID stays the last-deployed group, which is what the commands
	// fall back to when a config names only one.
	_ = r.ec.setEnvValue(ctx, envKeyEvalGroupID, created.ID)
	return created.ID, nil
}

// sameDefinition reports whether the locally authored definition already
// matches what the service holds.
//
// Only the keys the candidate declares are compared. The service enriches a
// definition when it is created — a rubric of nothing but `type` and
// `dimensions` comes back carrying data_schema, init_parameters and metrics it
// was never given — so comparing whole documents never matches and every
// deploy publishes a redundant version.
func sameDefinition(existing, candidate []byte) bool {
	extract := func(raw []byte) map[string]json.RawMessage {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil
		}
		def, ok := doc["definition"]
		if !ok {
			return nil
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(def, &fields); err != nil {
			return nil
		}
		return fields
	}

	onService, authored := extract(existing), extract(candidate)
	if onService == nil || authored == nil {
		return false
	}

	for key, want := range authored {
		got, ok := onService[key]
		if !ok || !equalJSON(got, want) {
			return false
		}
	}
	return true
}

// equalJSON compares two JSON values structurally, so key order and
// whitespace do not register as a change.
func equalJSON(a, b json.RawMessage) bool {
	var left, right any
	if err := json.Unmarshal(a, &left); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &right); err != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func versionFromRaw(raw []byte, fallback string) string {
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &doc); err == nil && doc.Version != "" {
		return doc.Version
	}
	return fallback
}

// versionKey holds the version resolved for an artifact at the last deploy.
func versionKey(kind, name string) string {
	return project.FingerprintKey(kind, name) + "_VERSION"
}

// idKey names the env entry holding a resolved id.
//
// Ids are per declaration. A single shared key works only while a config has
// one group: with two, the second deploy finds the first's id cached, confirms
// it exists, and hands it back for the wrong group.
func idKey(kind, name string) string {
	return project.FingerprintKey(kind, name) + "_ID"
}
