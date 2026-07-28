// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
		// Unchanged since the last deploy; reuse the recorded version.
		if version := r.ec.getEnvValue(ctx, versionKey("dataset", decl.Name)); version != "" {
			return version, false, nil
		}
	}

	// The upload helper scans a directory for the first .jsonl.
	dir := localPath
	if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
		dir = filepath.Dir(localPath)
	}

	ds, err := r.ec.datasetClient.UploadNewVersion(
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
	recreate bool,
) (string, error) {
	if group.ID != "" {
		return group.ID, nil
	}

	cached := r.ec.getEnvValue(ctx, envKeyEvalGroupID)
	if cached != "" && !recreate {
		if _, err := r.ec.evalClient.GetOpenAIEval(ctx, cached); err == nil {
			return cached, nil
		}
	}

	created, err := r.ec.evalClient.CreateOpenAIEval(ctx, buildEvalGroupRequest(&group))
	if err != nil {
		return "", err
	}
	_ = r.ec.setEnvValue(ctx, envKeyEvalGroupID, created.ID)
	return created.ID, nil
}

// sameDefinition compares only the definition body, ignoring server-assigned
// fields such as version and timestamps.
func sameDefinition(existing, candidate []byte) bool {
	extract := func(raw []byte) string {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			return ""
		}
		def, ok := doc["definition"]
		if !ok {
			return ""
		}
		var normalized any
		if err := json.Unmarshal(def, &normalized); err != nil {
			return ""
		}
		out, err := json.Marshal(normalized)
		if err != nil {
			return ""
		}
		return string(out)
	}

	a, b := extract(existing), extract(candidate)
	return a != "" && a == b
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
