// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"regexp"
	"strings"

	"azureaieval/internal/project"
)

var evalStateIDKey = regexp.MustCompile(
	`^(EVAL_FINGERPRINT_EVAL_[A-Z0-9_]+_[0-9A-F]{8}_ID|EVAL_SUBSTANCE_[0-9A-F]{16}_ID)(_[0-9A-F]{8})?$`)

func (ec *evalContext) deleteEvalState(ctx context.Context, id string) error {
	return ec.deletePrivateSelected(ctx, id, func(state map[string]string) []string {
		keys := []string{idKey("evalrun", id)}
		affected := map[string]bool{}
		surviving := map[string]bool{}
		for key, value := range state {
			parts := evalStateIDKey.FindStringSubmatch(key)
			if parts == nil {
				continue
			}
			base, scopeSuffix := parts[1], parts[2]
			if value != id {
				if value != "" {
					surviving[base] = true
				}
				continue
			}
			keys = append(keys, key)
			affected[base] = true
			if scopeSuffix != "" && strings.HasPrefix(base, project.EnvKeyFingerprintPrefix) {
				keys = append(keys, strings.TrimSuffix(base, "_ID")+scopeSuffix)
			}
		}
		for base := range affected {
			// A surviving scoped id still needs the original ownership marker
			// to route scopedValue to its suffixed key instead of the empty base.
			if surviving[base] {
				continue
			}
			keys = append(keys, base+project.EvalScopeSuffix)
			if strings.HasPrefix(base, project.EnvKeyFingerprintPrefix) {
				// Definition fingerprints are shared by name even when IDs are
				// scoped. A surviving eval still needs that baseline for edits.
				fingerprint := strings.TrimSuffix(base, "_ID")
				keys = append(keys, fingerprint, fingerprint+project.EvalScopeSuffix)
			}
		}
		return keys
	})
}
