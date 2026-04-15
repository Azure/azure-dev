// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/drone/envsubst"
)

// EnvResolver builds a merged environment for script execution using a 4-layer priority model:
//
//	Layer 4 (highest): secrets map values
//	Layer 3:           env map values (${EXPRESSION} substitution)
//	Layer 2:           azd environment values (.env + prior script outputs)
//	Layer 1 (lowest):  OS environment variables
type EnvResolver struct {
	osEnv  map[string]string
	azdEnv map[string]string
}

// NewEnvResolver creates a resolver seeded with OS and azd environment values.
func NewEnvResolver(azdEnv map[string]string) *EnvResolver {
	osEnv := make(map[string]string)
	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			osEnv[k] = v
		}
	}

	return &EnvResolver{
		osEnv:  osEnv,
		azdEnv: azdEnv,
	}
}

// Resolve builds the merged environment for a single script.
// The returned map contains every variable the script process should inherit.
func (r *EnvResolver) Resolve(sc *ScriptConfig) (map[string]string, error) {
	// Start with a copy of OS env (Layer 1)
	merged := maps.Clone(r.osEnv)

	// Layer 2: azd environment overrides OS
	maps.Copy(merged, r.azdEnv)

	// Layer 3: env map with ${EXPRESSION} substitution
	if sc.Env != nil {
		lookup := func(key string) string {
			if v, ok := merged[key]; ok {
				return v
			}
			return ""
		}

		for k, tmpl := range sc.Env {
			resolved, err := envsubst.Eval(tmpl, lookup)
			if err != nil {
				return nil, fmt.Errorf("resolving env variable %q: %w", k, err)
			}
			merged[k] = resolved
		}
	}

	// Layer 4: secrets override everything
	maps.Copy(merged, sc.Secrets)

	return merged, nil
}

// MergeOutputs adds script outputs to the azd environment layer so subsequent scripts see them.
func (r *EnvResolver) MergeOutputs(outputs map[string]OutputParameter) {
	for k, v := range outputs {
		r.azdEnv[k] = v.Value
	}
}
