// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"azureaieval/internal/messages"
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
	configPath := project.EvalConfigPath(evalDir)
	cfg, err := project.OpenEvalConfig(evalDir)
	if err != nil {
		return evalRef{}, err
	}

	if cfg != nil {
		if err := cfg.Validate(); err != nil {
			return evalRef{}, err
		}
		eval, err := cfg.Eval(nameOrID)
		switch {
		case err == nil:
			id := ec.recordedEvalID(ctx, eval.Name)
			if id == "" {
				return evalRef{}, messages.EvalNotDeployedYet(eval.Name)
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
func (ec *evalContext) recordedEvalID(ctx context.Context, evalName string) string {
	return ec.getEnvValue(ctx, idKey("eval", evalName))
}
