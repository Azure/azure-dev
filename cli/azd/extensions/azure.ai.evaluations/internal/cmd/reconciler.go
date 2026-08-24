// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
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

	// claimed holds the evals this deploy has already settled, so a second
	// declaration cannot adopt one. Substance keys are never removed from the
	// environment, so one left behind by an earlier edit still points at a live
	// eval -- and adopting it renames that eval and leaves the declaration that
	// asked for it sharing the other one's runs.
	claimed map[string]bool

	// decided holds each declaration's digests, so reservation and
	// reconciliation cannot answer the question differently.
	decided map[string]evalDecision
}

var _ project.Reconciler = (*evalReconciler)(nil)

func newEvalReconciler(ctx context.Context) (project.Reconciler, error) {
	ec, err := newEvalContext(ctx, "")
	if err != nil {
		return nil, err
	}
	return &evalReconciler{ec: ec}, nil
}

// claim records an eval this deploy has settled on. Built lazily, because the
// reconciler is also constructed literally in a few places.
func (r *evalReconciler) claim(id string) {
	if r.claimed == nil {
		r.claimed = map[string]bool{}
	}
	r.claimed[id] = true
}

// ReserveDeclared marks the evals these declarations already resolve to as
// spoken for.
//
// Claiming only as each declaration finished made adoption depend on file
// order: a declaration listed above the one that owns an eval would adopt it,
// rename it, and end up sharing its runs. Reserving up front is the same guard
// without the ordering. A name the environment holds no id for reserves
// nothing, which is what leaves a genuine rename free to adopt.
//
// A declaration that is going to be recreated reserves nothing either: it is
// about to abandon that eval, and holding it back would refuse the rename that
// legitimately continues it.
//
// An explicit `id:` is reserved before any of that, by reserveExplicitIDs.
func (r *evalReconciler) ReserveDeclared(ctx context.Context, groups []project.Eval) {
	r.reserveExplicitIDs(groups)
	for i := range groups {
		decision, err := r.decide(ctx, groups[i])
		if err != nil {
			// Nothing decided, so nothing skipped. The error surfaces from
			// EnsureEval, where it can fail the deploy.
			continue
		}
		id := r.ec.getEnvValue(ctx, idKey("eval", groups[i].Name))
		if id == "" || decision.recreate {
			continue
		}
		r.claim(id)
	}
}

// reserveExplicitIDs claims every eval an author named outright.
//
// Separate, and first, because it reads nothing: an explicit `id:` is the
// author naming the eval, so there is no decision to make and nothing that
// could release it. The loop below consults the recorded environment, and a
// read that fails skips its entry -- a reservation that can be skipped is not a
// reservation.
//
// Without this, a pinned id was claimed only once its own declaration was
// reached, so file order decided the outcome: a declaration listed above it
// could reach the same eval through digestIDKey first, and the two ended up
// sharing one eval and one run history. That is the collision this pre-pass
// exists to prevent, and the pinned declaration was the one that lost it.
func (r *evalReconciler) reserveExplicitIDs(groups []project.Eval) {
	for i := range groups {
		if groups[i].ID != "" {
			r.claim(groups[i].ID)
		}
	}
}

// evalDecision is what one declaration's digests settled.
type evalDecision struct {
	digest     string
	definition string
	recreate   bool
}

// decide hashes a declaration both ways and says whether its substance changed
// since the last deploy, answering the same way every time it is asked.
//
// Remembered per name because two callers ask: reservation, before anything is
// reconciled, and EnsureEval itself. The recorded baseline is read over gRPC
// and a read that failed answers "", so asking twice let one transient failure
// leave an eval unreserved and then reused -- two declarations on one id, which
// is the collision reservation exists to stop.
//
// digest identifies the declaration and keys the id a rename looks up.
// definition is what the service stores, and is what the recreate comparison is
// made against. The recorded baseline is the definition from the build that
// split them on, and the full digest from every build before: equality with
// either says the declaration is what was deployed.
func (r *evalReconciler) decide(ctx context.Context, group project.Eval) (evalDecision, error) {
	if decided, ok := r.decided[group.Name]; ok {
		return decided, nil
	}

	digest, err := project.FingerprintGroup(group)
	if err != nil {
		return evalDecision{}, err
	}
	definition, err := project.FingerprintDefinition(group)
	if err != nil {
		return evalDecision{}, err
	}
	prior := r.ec.getEnvValue(ctx, project.FingerprintKey("eval", group.Name))

	decided := evalDecision{
		digest:     digest,
		definition: definition,
		recreate:   prior != "" && prior != definition && prior != digest,
	}
	if r.decided == nil {
		r.decided = map[string]evalDecision{}
	}
	r.decided[group.Name] = decided
	return decided, nil
}

// evalDigests hashes a declaration both ways and says whether its substance
// changed since the last deploy.
//
// digest identifies the declaration and keys the id a rename looks up.
// definition is what the service stores, and is what the recreate comparison
// is made against. The recorded baseline is the definition from the build that
// split them on, and the full digest from every build before: equality with
// either says the declaration is what was deployed.
func (r *evalReconciler) evalDigests(
	ctx context.Context,
	group project.Eval,
) (digest, definition string, recreate bool, err error) {
	decided, err := r.decide(ctx, group)
	if err != nil {
		return "", "", false, err
	}
	return decided.digest, decided.definition, decided.recreate, nil
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
				// A pin settles which version to use, not whether it is still
				// there. Skipping the service entirely let a deleted version
				// report as unchanged while the eval pointed at nothing. Only a
				// confirmed 404 refuses: anything else leaves the pin alone
				// rather than failing a deploy on a transient read.
				if _, err := r.ec.datasetClient.GetDataset(
					ctx, decl.Name, decl.Version, ProjectEndpointAPIVersion,
				); err != nil && dataset_api.IsNotFound(err) {
					return "", false, messages.DatasetVersionNotFoundWithHint(decl.Name, decl.Version)
				}
				// Deliberately not recorded. The key means "the version this file's
				// content published", which is what the drift check compares
				// against: writing the pin here made removing it later read as
				// somebody having published behind the configuration's back, and
				// failed the deploy. The run reads the pin from the declaration.
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
		text := scanner.Text()
		if line == 1 {
			// PowerShell's `>` and Set-Content write a byte order mark, so a
			// file a reader produced by redirecting output starts with bytes
			// that are not JSON. The upload path already drops it, and refusing
			// here what deploy would accept sends them hunting a row that is
			// fine.
			text = project.TrimBOM(text)
		}
		text = strings.TrimSpace(text)
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
// A definition reaches this three ways: written in the configuration, named as
// a file, or neither -- in which case the evaluator has to already exist on the
// service. The first two are the same publish once the rubric is in hand; they
// differ only in what there is to hash.
func (r *evalReconciler) EnsureEvaluator(
	ctx context.Context,
	decl project.EvaluatorDecl,
	localPath string,
) (string, bool, error) {
	var body json.RawMessage
	var digest string

	switch {
	case decl.Definition != nil:
		// Also how a `$ref` to a rubric file arrives: resolution has already
		// spliced the file's keys in, so there is nothing left to read.
		raw, err := json.Marshal(decl.Definition)
		if err != nil {
			return "", false, messages.EvaluatorProblem(decl.Name, err)
		}
		if body, err = normalizeRubricBody(decl.Name, raw); err != nil {
			return "", false, messages.EvaluatorProblem(decl.Name, err)
		}
		digest = project.FingerprintBytes(body)

	case localPath == "":
		raw, err := r.ec.evalClient.GetEvaluatorRaw(
			ctx, decl.Name, decl.Version, ProjectEndpointAPIVersion,
		)
		if err != nil {
			return "", false, messages.EvaluatorNotLocalNorFound(decl.Name, err)
		}
		return versionFromRaw(raw, decl.Version), false, nil

	default:
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

		if body, err = normalizeRubricBody(decl.Name, raw); err != nil {
			return "", false, messages.EvaluatorProblem(decl.Name, err)
		}

		if digest, err = project.Fingerprint(localPath); err != nil {
			return "", false, messages.EvaluatorSource(localPath, err)
		}
	}

	// The author's own definition decides whether there is anything to publish.
	// Comparing against the service cannot: it enriches a definition with
	// fields nobody authored, so sameDefinition only looks for authored keys on
	// the service and a key the author *deleted* — a pass_threshold, say — is
	// still there to be found, and the deletion never publishes.
	digestKey := project.FingerprintKey("evaluator", decl.Name)
	prior := r.ec.getEnvValue(ctx, digestKey)
	authorEdited := prior != "" && prior != digest

	// Compare against the definition already on the service.
	var known json.RawMessage
	existing, err := r.ec.evalClient.GetEvaluatorRaw(
		ctx, decl.Name, "", ProjectEndpointAPIVersion,
	)
	// A read that failed is not a read that found nothing: falling through
	// publishes a new version with no drift check, over whatever is already
	// there. Only a confirmed absence is a first publish.
	if err != nil && !eval_api.IsNotFound(err) {
		return "", false, messages.CheckingEvaluatorExists(decl.Name, err)
	}
	if err == nil {
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
		r.claim(group.ID)
		return group.ID, false, nil
	}

	// Evals are immutable, so a change to the eval's own substance — evaluators,
	// dataset, target, level — needs a new eval. Name and description are
	// excluded from the digest and pushed in place instead, and so are
	// max_samples and source:, which the run carries rather than the eval.
	digest, definition, recreate, err := r.evalDigests(ctx, group)
	if err != nil {
		return "", false, err
	}
	key := project.FingerprintKey("eval", group.Name)

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
		adopted, err := r.adoptRenamed(ctx, group, digest)
		if err != nil {
			return "", false, err
		}
		if adopted != "" {
			cached = adopted
		}
	}
	if cached != "" && !recreate {
		remote, err := r.ec.evalClient.GetOpenAIEval(ctx, cached)
		if err != nil && !eval_api.IsNotFound(err) {
			// A read that failed is not an eval that is gone. Falling through
			// on a 429, a 503 or an expired token would create a second eval
			// and overwrite the recorded id, forking for good the run history
			// this lookup exists to keep.
			return "", false, err
		}
		if err == nil {
			// Reusing the eval is not the same as leaving it alone: name and
			// description are excluded from the digest because they must not
			// split a history, which makes this the only place an edit to
			// either of them can reach the service.
			r.pushMutable(ctx, cached, group, remote)

			// Record the definition on reuse as well, otherwise an eval deployed
			// before fingerprinting existed never establishes a baseline and
			// later edits go undetected. The identity digest is recorded beside
			// it, which is what recognizes this declaration after a rename.
			r.ec.remember(ctx, key, definition)
			r.ec.remember(ctx, idKey("eval", group.Name), cached)
			r.ec.remember(ctx, digestIDKey(digest), cached)
			r.ec.remember(ctx, envKeyEvalID, cached)
			r.claim(cached)
			return cached, false, nil
		}
	}

	created, err := r.ec.evalClient.CreateOpenAIEval(ctx, req)
	if err != nil {
		return "", false, err
	}
	r.ec.remember(ctx, key, definition)
	r.ec.remember(ctx, idKey("eval", group.Name), created.ID)
	r.ec.remember(ctx, digestIDKey(digest), created.ID)
	// EVAL_ID stays the last-deployed eval. Nothing reads it to decide which
	// eval a command means, because every deploy writes it and it cannot say
	// which declaration it belongs to; it is here for anything outside this
	// extension that wants the id of what was just deployed.
	r.ec.remember(ctx, envKeyEvalID, created.ID)
	r.claim(created.ID)
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
) (string, error) {
	id := r.ec.getEnvValue(ctx, digestIDKey(digest))
	if id == "" {
		return "", nil
	}
	if r.claimed[id] {
		// Another declaration in this same file already settled on it. Adopting
		// it here would rename that eval and leave both declarations sharing
		// one id and one run history, which is worse than creating a second.
		return "", nil
	}
	remote, err := r.ec.evalClient.GetOpenAIEval(ctx, id)
	if err != nil {
		if eval_api.IsNotFound(err) {
			// The eval it used to be called is genuinely gone, so there is
			// nothing to adopt and the caller creates one.
			return "", nil
		}
		return "", err
	}
	r.pushMutable(ctx, id, group, remote)
	return id, nil
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
	if _, err := r.ec.evalClient.UpdateOpenAIEval(ctx, id, &eval_api.UpdateOpenAIEvalRequest{
		Name:     group.Name,
		Metadata: desired,
	}); err != nil {
		// Deliberately not fatal: a name or description that did not travel
		// leaves the eval usable, and failing the deploy over it would be
		// worse. It still has to be findable, so --debug can see it.
		log.Printf("[reconcile] updating eval %s name/description: %v", id, err)
	}
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
