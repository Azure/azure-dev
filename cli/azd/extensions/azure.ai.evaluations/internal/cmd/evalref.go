// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"sort"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
)

// evalRef is what `--eval` resolved to: the service id, plus the declaration
// behind it when there was one.
//
// Commands need both. The id is what every route under {eval_id} takes, and the
// declaration is what says which dataset and target a new run should use.
type evalRef struct {
	ID         string
	Eval       *project.Eval
	Config     *project.EvalConfig
	ConfigPath string
}

// Declared reports whether the reference came from the configuration.
func (r evalRef) Declared() bool { return r.Eval != nil }

// resolveEvalRef turns `--eval` into an id.
//
// One flag takes a name or an id, matching `azd ai training job show`, whose
// --name is documented as "Job name/ID". Name is tried first, in three cases:
// a name in evals: with a recorded id resolves to it; a name in evals: with
// none fails fast naming `azd up` rather than returning a service 404; and
// anything else is sent as an id.
//
// Ids matter because an eval created by `azd ai eval create` has no evals:
// entry, and because the environment records one id per name, so editing a
// declaration leaves every run of the previous eval reachable only by id.
func (ec *evalContext) resolveEvalRef(
	ctx context.Context,
	evalDir, nameOrID string,
) (evalRef, error) {
	configPath := project.ResolveEvalConfigPath(evalDir)
	cfg, err := project.OpenEvalConfig(evalDir)
	if err != nil {
		return evalRef{}, err
	}

	if cfg != nil {
		if err := cfg.ValidateForLookup(); err != nil {
			return evalRef{}, err
		}
		eval, err := cfg.Eval(nameOrID)
		switch {
		case err == nil:
			id := ec.recordedEvalID(ctx, cfg, eval.Name)
			if id == "" {
				// Nothing recorded is not the same as nothing published. The
				// id is kept in the azd environment, so a run against
				// --project-endpoint with no project, or a `create` that ran
				// before the environment existed, leaves a published eval
				// with no note of it. The service lists evals by name, so
				// ask it before deciding this was never deployed.
				//
				// Refused rather than guessed when a name carries several, as
				// `eval delete` already does. Newest-wins is fine for showing
				// something, but this id also reaches `run start`, and grading
				// against the wrong definition produces results that look
				// right and answer a different question.
				ids, err := ec.evalIDsNamed(ctx, eval.Name)
				if err != nil {
					// A listing that failed is not a listing that came
					// back empty. Falling through to "not deployed"
					// sends the reader to republish an eval that
					// already exists -- which is what a listing failing
					// under concurrent `run start` actually produced.
					return evalRef{}, err
				}
				if len(ids) > 1 {
					return evalRef{}, messages.AmbiguousEvalName(eval.Name, ids)
				}
				if len(ids) == 1 {
					id = ids[0]
				}
			}
			if id == "" {
				// With no environment there was nowhere an id could have been
				// recorded, so telling the reader to deploy again would not
				// help. Only said when azd confirmed there is none.
				if ec.confirmedNoAzdEnvironment(ctx) {
					return evalRef{}, messages.NoEnvironmentToRememberEval(eval.Name)
				}
				// That call recovers the environment name when the first
				// lookup missed it -- a transient failure in newEvalContext
				// leaves it empty, and recordedEvalID answers "" without
				// asking when it is. Now that there is a name, ask properly
				// before reporting a deployed eval as missing.
				if id = ec.recordedEvalID(ctx, cfg, eval.Name); id == "" {
					return evalRef{}, messages.EvalNotDeployedYet(
						eval.Name, ec.deployCommand(ctx))
				}
			}
			return evalRef{ID: id, Eval: eval, Config: cfg, ConfigPath: configPath}, nil
		case nameOrID == "":
			// No name to fall back on, so the configuration's own complaint —
			// none declared, or several to choose between — is the answer.
			return evalRef{}, err
		}
	}

	if nameOrID == "" {
		return evalRef{}, messages.NoEvalNamedOrDeclared(configPath)
	}
	// Not a declared name, so it is an id.
	return evalRef{ID: nameOrID}, nil
}

// recordedEvalID reads the id `azd up` stored for a declared eval.
func (ec *evalContext) recordedEvalID(ctx context.Context, cfg *project.EvalConfig, evalName string) string {
	for _, key := range evalIDKeys(cfg, evalName) {
		if id := ec.getEnvValue(ctx, key); id != "" {
			return id
		}
	}
	return ""
}

// evalIDKeys lists the env entries that may hold this eval's id, most specific
// first.
//
// The per-name entry is what the extension writes. EVAL_ID is the documented
// way to point a config at an eval that already exists, created in the portal
// or by another tool, so it stays readable -- but only when the configuration
// declares a single eval. With more than one there is no way to tell which eval
// a shared entry refers to, and reading it anyway is what let a second eval
// adopt the first one's id.
func evalIDKeys(cfg *project.EvalConfig, name string) []string {
	keys := []string{idKey("eval", name)}
	if cfg != nil && len(cfg.Evals) == 1 {
		keys = append(keys, envKeyEvalID)
	}
	return keys
}

// evalIDNamed finds the id of the eval the service lists under this name.
//
// Evals are addressed by id, but every listing reports a name, so a name is
// what a reader has to hand. Returns empty when nothing matches, leaving the
// caller's not-found reporting alone.
//
// The newest match wins when a name is carried by several. An eval is
// immutable, so editing a declaration creates another one under the same name,
// and the newest is the one the configuration currently describes.
//
// A listing that failed answers empty here, unlike in resolveEvalRef: both
// callers reach this only after the service already returned 404 for the id
// they were given, and that refusal is what they report.
func (ec *evalContext) evalIDNamed(ctx context.Context, name string) string {
	ids, err := ec.evalIDsNamed(ctx, name)
	if err != nil || len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// evalIDsNamed finds every eval the service lists under this name, newest
// first, so a caller that must not guess can see the ambiguity.
//
// The order is established here rather than taken from the service, which does
// not promise one. timestampString normalizes both shapes the service uses for
// created_at to RFC3339 UTC, and those sort chronologically as text. An eval
// whose timestamp is missing or unparseable sorts last rather than winning by
// accident.
func (ec *evalContext) evalIDsNamed(ctx context.Context, name string) ([]string, error) {
	list, err := ec.evalClient.ListOpenAIEvals(ctx, 0)
	if err != nil {
		return nil, messages.ListingEvals(err)
	}
	if list == nil {
		return nil, nil
	}
	return idsNamedIn(list, name), nil
}

// idsNamedIn picks the evals carrying this name, newest first.
//
// timestampString normalizes both shapes the service uses for created_at to
// RFC3339 UTC, and those sort chronologically as text. An eval whose timestamp
// is missing or unparseable sorts last rather than winning by accident.
func idsNamedIn(list *eval_api.OpenAIEvalList, name string) []string {
	var matches []eval_api.OpenAIEval
	for _, e := range list.Data {
		if e.Name == name {
			matches = append(matches, e)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return timestampString(matches[i].CreatedAt) > timestampString(matches[j].CreatedAt)
	})
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.ID)
	}
	return ids
}
