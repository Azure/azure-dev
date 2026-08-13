// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
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
				return "", false, messages.DatasetNotLocalNorFound(decl.Name, err)
			}
			if len(list.Value) == 0 {
				return "", false, messages.DatasetNotLocalNorRegistered(decl.Name)
			}
			version = dataset_api.LatestVersion(list.Value)
		} else if _, err := r.ec.datasetClient.GetDataset(
			ctx, decl.Name, version, ProjectEndpointAPIVersion,
		); err != nil {
			return "", false, messages.DatasetVersionNotFoundWithHint(decl.Name, version)
		}

		// Recorded so a run reads the version reconciliation settled on. Without
		// this a pin is honoured at deploy and then ignored at run time, which
		// scores different rows than the ones the author asked for.
		r.ec.remember(ctx, versionKey("dataset", decl.Name), version)
		return version, false, nil
	}

	if _, err := os.Stat(localPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, messages.DatasetNotGeneratedYet(decl.Name, localPath)
		}
		return "", false, messages.DatasetSource(localPath, err)
	}

	// A malformed row is only noticed once the service tries to evaluate it,
	// by which point a version has been published and the eval points at
	// it. Reading the file here costs nothing and names the offending line.
	if err := validateJSONL(localPath); err != nil {
		return "", false, messages.DatasetProblem(decl.Name, err)
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

	// Uploaded by the path the author declared. Collapsing a file to its
	// directory would upload whichever .jsonl sorts first, while the
	// fingerprint below still describes the declared one.
	dir := localPath

	// A declared version is the version to publish, not one to count from.
	// Reaching here means the content differs from what that version holds, so
	// republishing over it would change a version the author pinned.
	if decl.Version != "" {
		ds, err := r.ec.datasetClient.UploadVersion(
			ctx, decl.Name, decl.Version, dir, ProjectEndpointAPIVersion,
		)
		if err != nil {
			if dataset_api.IsVersionConflict(err) {
				return "", false, messages.DatasetVersionConflict(decl.Name, decl.Version)
			}
			return "", false, err
		}
		r.ec.remember(ctx, key, digest)
		r.ec.remember(ctx, versionKey("dataset", decl.Name), ds.Version)
		return ds.Version, true, nil
	}

	// UploadNextVersion discovers the currently registered version when none is
	// declared, so the upload does not restart at 1.0 and collide.
	ds, err := r.ec.datasetClient.UploadNextVersion(
		ctx, decl.Name, decl.Version, dir, ProjectEndpointAPIVersion,
	)
	if err != nil {
		return "", false, err
	}

	r.ec.remember(ctx, key, digest)
	r.ec.remember(ctx, versionKey("dataset", decl.Name), ds.Version)
	r.ec.remember(ctx, envKeyDatasetVersion, ds.Version)

	return ds.Version, true, nil
}

// validateJSONL checks that every row is a JSON object before the file is
// published.
//
// The service accepts the upload whatever the bytes are, so a typo becomes a
// registered version, an eval bound to it, and a run that fails on a row
// nobody has looked at. Blank lines are skipped: they are not rows.
func validateJSONL(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return messages.ReadingPath(path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// A row carrying a whole conversation runs well past the 64KB default.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	rows := 0
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(text), &row); err != nil {
			return messages.JSONLRowInvalid(path, line, err)
		}
		if len(row) == 0 {
			return messages.JSONLRowEmpty(path, line)
		}
		rows++
	}
	if err := scanner.Err(); err != nil {
		return messages.ReadingPath(path, err)
	}
	if rows == 0 {
		return messages.JSONLNoRows(path)
	}
	return nil
}

func (r *evalReconciler) checkDatasetDrift(
	ctx context.Context,
	name, recorded string,
) error {
	latest, err := r.latestDatasetVersion(ctx, name)
	if err != nil {
		// The whole point of this check is to catch a version published behind
		// our back. A listing we could not read is not evidence there was none.
		return err
	}
	if latest == "" || latest == recorded {
		return nil
	}
	if !dataset_api.VersionGreater(latest, recorded) {
		return nil
	}
	return messages.DatasetDrifted(name, latest, recorded)
}

// latestDatasetVersion reports the newest registered version. A dataset the
// service does not know, and a listing that has not caught up, both report an
// empty version and no error; anything else is returned.
func (r *evalReconciler) latestDatasetVersion(ctx context.Context, name string) (string, error) {
	list, err := r.ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
	if err != nil {
		if dataset_api.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	if list == nil || len(list.Value) == 0 {
		return "", nil
	}
	return dataset_api.LatestVersion(list.Value), nil
}

// EnsureEvaluator publishes a new version when the local definition differs
// from what the service holds.
//
// The two kinds of evaluator are told apart by the source's extension: `.py`
// is code, anything else is a rubric. They also detect change differently. A
// rubric definition comes back inline, so it is compared directly; a code
// definition's source is not read back in a form worth comparing, so a
// fingerprint of the script is kept in the azd environment, the same way
// datasets work.
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
			return "", false, messages.EvaluatorNotLocalNorFound(decl.Name, err)
		}
		return versionFromRaw(raw, decl.Version), false, nil
	}

	if _, err := os.Stat(localPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, messages.EvaluatorNotGeneratedYet(decl.Name, localPath)
		}
		return "", false, messages.EvaluatorSource(localPath, err)
	}

	raw, err := project.ReadFileNoBOM(localPath)
	if err != nil {
		return "", false, messages.EvaluatorSource(localPath, err)
	}

	body, err := normalizeRubricBody(decl.Name, raw)
	if err != nil {
		return "", false, messages.EvaluatorProblem(decl.Name, err)
	}

	// The author's own file decides whether there is anything to publish.
	// Comparing against the service cannot: it enriches a definition with
	// fields nobody authored, so sameDefinition only looks for authored keys on
	// the service and a key the author *deleted* — a pass_threshold, say — is
	// still there to be found, and the deletion never publishes.
	digest, err := project.Fingerprint(localPath)
	if err != nil {
		return "", false, messages.EvaluatorSource(localPath, err)
	}
	digestKey := project.FingerprintKey("evaluator", decl.Name)
	prior := r.ec.getEnvValue(ctx, digestKey)
	authorEdited := prior != "" && prior != digest

	// Compare against the definition already on the service.
	var known json.RawMessage
	if existing, err := r.ec.evalClient.GetEvaluatorRaw(
		ctx, decl.Name, "", ProjectEndpointAPIVersion,
	); err == nil {
		remote := versionFromRaw(existing, "")
		if !authorEdited && sameDefinition(existing, body) {
			// Nothing to publish, but the version is still worth recording:
			// it is what a later deploy compares against to notice that
			// someone moved the evaluator on from here.
			if remote != "" {
				r.ec.remember(ctx, versionKey("evaluator", decl.Name), remote)
			}
			r.ec.remember(ctx, digestKey, digest)
			return versionFromRaw(existing, decl.Version), false, nil
		}

		// The definitions differ, which means either the local file changed
		// or someone published a version outside the repo. The version
		// recorded at the last deploy is what tells them apart, and
		// publishing over the second case would bury an intentional change
		// under one nobody asked for.
		if recorded := r.ec.getEnvValue(ctx, versionKey("evaluator", decl.Name)); recorded != "" {
			if err := checkEvaluatorDrift(decl.Name, recorded, remote); err != nil {
				return "", false, err
			}
		}

		// What that read saw is what keeps the publish from being answered
		// with it again.
		known = existing
	}

	created, err := r.ec.evalClient.CreateEvaluatorVersion(
		ctx, decl.Name, body, known, ProjectEndpointAPIVersion,
	)
	if err != nil {
		return "", false, err
	}
	r.awaitEvaluatorReadable(ctx, decl.Name, created.Version)
	r.ec.remember(ctx, versionKey("evaluator", decl.Name), created.Version)
	r.ec.remember(ctx, digestKey, digest)
	return created.Version, true, nil
}

// checkEvaluatorDrift fails when the service holds a newer version than the
// one recorded at the last deploy.
//
// It is asked only when the local definition and the remote one disagree,
// which on its own says nothing about who moved: the author may have edited
// the file, or someone may have published a version from outside the repo.
// The recorded version settles it, and the difference matters because
// publishing is how this reconciler resolves a disagreement — doing that over
// a version somebody deliberately published would bury their change under one
// nobody asked for, with `azd up` reporting success.
//
// The remote version is passed in rather than listed, because the version
// listing lags a publish and would report an evaluator as un-drifted for the
// first seconds of its newest version's life.
func checkEvaluatorDrift(name, recorded, remote string) error {
	recordedNumber, err := strconv.Atoi(recorded)
	if err != nil {
		return nil
	}
	remoteNumber, err := strconv.Atoi(remote)
	if err != nil || remoteNumber <= recordedNumber {
		return nil
	}
	return messages.EvaluatorDrifted(name, remote, recorded)
}

// evaluatorPropagation bounds the wait for a freshly published evaluator to
// become usable.
//
// A create returns before the version is resolvable everywhere, and the very
// next step of a deploy is EnsureEval, which names the evaluator in a testing
// criterion. Creating the eval inside that window fails with "The evaluator X
// was not found" — a confusing error, because the evaluator was published
// seconds earlier and is plainly there by the time anyone looks. The observed
// gap is under a second, so the poll is frequent and the cap is generous
// enough to absorb a slow day without stalling a deploy on an evaluator that
// is genuinely missing.
const (
	evaluatorPropagationTimeout  = 30 * time.Second
	evaluatorPropagationInterval = 250 * time.Millisecond
)

// awaitEvaluatorReadable polls until a published version is resolvable, or the
// cap passes.
//
// Two reads have to agree, because they are not backed by the same view. The
// direct read goes consistent almost immediately; the version listing lags it
// by seconds, the same way the dataset listing does. A live publish was
// observed reading back at 03:06:58 and still failing eval creation at
// 03:06:59, so waiting on the direct read alone leaves exactly the race this
// exists to close. The listing is the slower of the two and therefore the one
// worth waiting on.
//
// A timeout is not an error. The wait is a courtesy that makes the common case
// reliable; if it never succeeds, the create that follows will report the real
// problem with far more context than a wait that gave up could.
func (r *evalReconciler) awaitEvaluatorReadable(ctx context.Context, name, version string) {
	if version == "" {
		return
	}
	deadline := time.Now().Add(evaluatorPropagationTimeout)
	for {
		if r.evaluatorVersionResolvable(ctx, name, version) {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(evaluatorPropagationInterval):
		}
	}
}

// evaluatorVersionResolvable reports whether a version can be both read
// directly and found in the listing.
func (r *evalReconciler) evaluatorVersionResolvable(
	ctx context.Context,
	name, version string,
) bool {
	if _, err := r.ec.evalClient.GetEvaluatorRaw(
		ctx, name, version, ProjectEndpointAPIVersion,
	); err != nil {
		return false
	}

	list, err := r.ec.evalClient.ListEvaluatorVersions(
		ctx, name, ProjectEndpointAPIVersion,
	)
	if err != nil || list == nil {
		return false
	}
	for _, entry := range list.Value {
		if entry.Version == version {
			return true
		}
	}
	return false
}

// EnsureEval creates the eval when it has never been deployed, or when its own
// declaration changed. Evals are immutable, so a declaration change means a new
// eval and a new id.
//
// What an eval's references *resolve to* is deliberately not a reason to
// recreate it: an evaluator tracking latest that publishes a new version leaves
// every eval that runs it alone, which is what keeps a rubric edit comparable
// against the runs taken before it.
func (r *evalReconciler) EnsureEval(
	ctx context.Context,
	group project.Eval,
	datasetPath string,
) (string, bool, error) {
	if group.ID != "" {
		return group.ID, false, nil
	}

	// Evals are immutable, so a change to the eval's own substance — evaluators,
	// dataset, source, target, level — needs a new eval. Name and description are
	// excluded from the digest and pushed in place instead.
	recreate := false
	digest, err := project.FingerprintGroup(group)
	if err != nil {
		return "", false, err
	}
	key := project.FingerprintKey("eval", group.Name)
	if prior := r.ec.getEnvValue(ctx, key); prior != "" && prior != digest {
		recreate = true
	}

	// Building the request is also what checks the declaration against the
	// dataset's columns, so it happens before the reuse decision: a dataset can
	// lose a column an evaluator needs without the eval's own declaration
	// changing, and reusing the eval would let that reach a run unreported.
	req, err := buildEvalRequest(
		&group,
		r.ec.evaluatorSchemas(ctx),
		datasetColumnsFromPath(datasetPath),
	)
	if err != nil {
		return "", false, err
	}

	cached := r.ec.getEnvValue(ctx, idKey("eval", group.Name))
	if cached == "" && !recreate {
		// Nothing recorded under this name, but the substance may already be
		// deployed under the name it had before. The environment records the id
		// against the digest as well, which is what recognizes a rename rather
		// than reading it as a delete plus an add.
		if adopted := r.adoptRenamed(ctx, group, digest); adopted != "" {
			cached = adopted
		}
	}
	if cached != "" && !recreate {
		if remote, err := r.ec.evalClient.GetOpenAIEval(ctx, cached); err == nil {
			// Reusing the eval is not the same as leaving it alone: name and
			// description are excluded from the digest because they must not
			// split a history, which makes this the only place an edit to
			// either of them can reach the service.
			r.pushMutable(ctx, cached, group, remote)

			// Record the digest on reuse as well, otherwise an eval deployed
			// before fingerprinting existed never establishes a baseline and
			// later edits go undetected.
			r.ec.remember(ctx, key, digest)
			r.ec.remember(ctx, idKey("eval", group.Name), cached)
			r.ec.remember(ctx, digestIDKey(digest), cached)
			r.ec.remember(ctx, envKeyEvalID, cached)
			return cached, false, nil
		}
	}

	created, err := r.ec.evalClient.CreateOpenAIEval(ctx, req)
	if err != nil {
		return "", false, err
	}
	r.ec.remember(ctx, key, digest)
	r.ec.remember(ctx, idKey("eval", group.Name), created.ID)
	r.ec.remember(ctx, digestIDKey(digest), created.ID)
	// EVAL_ID stays the last-deployed eval, which is what the commands
	// fall back to when a config names only one.
	r.ec.remember(ctx, envKeyEvalID, created.ID)
	return created.ID, true, nil
}

// adoptRenamed reclaims the eval this declaration used to be called, so a
// rename keeps the id and every run under it rather than forking the history.
//
// The name is what UpdateEvalParametersBody reaches, so the new one is pushed
// to the service.
func (r *evalReconciler) adoptRenamed(
	ctx context.Context,
	group project.Eval,
	digest string,
) string {
	id := r.ec.getEnvValue(ctx, digestIDKey(digest))
	if id == "" {
		return ""
	}
	remote, err := r.ec.evalClient.GetOpenAIEval(ctx, id)
	if err != nil {
		return ""
	}
	r.pushMutable(ctx, id, group, remote)
	return id
}

// pushMutable sends the half of a declaration the service treats as mutable.
//
// Substance never travels this way — an edit that touches it is a new eval.
// Name and description are left out of the fingerprint precisely because they
// cost nothing to change and must not split a run history, so they are
// reconciled here rather than ignored, and the eval keeps its id and every run
// under it.
//
// A failure is not fatal. The eval is still the right one and the declaration
// still resolves; it just reads under its old wording in the portal until the
// next deploy.
func (r *evalReconciler) pushMutable(
	ctx context.Context,
	id string,
	group project.Eval,
	remote *eval_api.OpenAIEval,
) {
	if remote == nil {
		return
	}
	desired := withDescription(remote.Metadata, group.Description)
	if remote.Name == group.Name && maps.Equal(remote.Metadata, desired) {
		return
	}
	_, _ = r.ec.evalClient.UpdateOpenAIEval(ctx, id, &eval_api.UpdateOpenAIEvalRequest{
		Name:     group.Name,
		Metadata: desired,
	})
}

// withDescription applies the declaration's description to the metadata the
// service already holds, leaving every other key alone — including any the
// service added itself, which a replacing update would otherwise drop.
func withDescription(held map[string]string, description string) map[string]string {
	merged := make(map[string]string, len(held)+1)
	maps.Copy(merged, held)
	if description == "" {
		delete(merged, metaDescription)
	} else {
		merged[metaDescription] = description
	}
	return merged
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

// recordDeployedDataset records the state a deploy would have left behind for a
// dataset that the service has already registered and that already has a local
// copy.
//
// `azd up` decides whether to publish by comparing the local file against a
// fingerprint held in the environment. A generated dataset arrives with no such
// fingerprint, so without this the first deploy after `generate` reads the file
// as new and publishes a second version identical to the one the job just
// registered. Only a local edit should produce version 2.
//
// Best effort: failing to record costs a redundant version, not correctness.
func (ec *evalContext) recordDeployedDataset(
	ctx context.Context,
	name, localPath, version string,
) {
	digest, err := project.Fingerprint(localPath)
	if err != nil {
		return
	}
	ec.remember(ctx, project.FingerprintKey("dataset", name), digest)
	if version != "" {
		ec.remember(ctx, versionKey("dataset", name), version)
	}
}

// idKey names the env entry holding a resolved id.
//
// Ids are per declaration. A single shared key works only while a config has
// one group: with two, the second deploy finds the first's id cached, confirms
// it exists, and hands it back for the wrong group.
func idKey(kind, name string) string {
	return project.FingerprintKey(kind, name) + "_ID"
}

// digestIDKey records an eval's id against its substance, which is what lets a
// renamed declaration find the eval it already deployed. Keyed by a prefix of
// the digest, because the whole hash makes an unreadable environment variable.
func digestIDKey(digest string) string {
	return "EVAL_SUBSTANCE_" + strings.ToUpper(digest[:16]) + "_ID"
}
