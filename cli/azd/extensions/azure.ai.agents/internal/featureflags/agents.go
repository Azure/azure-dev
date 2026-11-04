// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package featureflags

import (
	"fmt"
	"io"
	"os"
	"sync"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

var enabledNotices = struct {
	sync.Mutex
	seen map[string]struct{}
}{seen: make(map[string]struct{})}

// Require announces an enabled preview feature once per process and rejects a disabled feature.
func Require(feature Feature) error {
	return RequireWithWriter(feature, os.Stdout)
}

// RequireWithWriter announces an enabled preview feature once per process using writer.
func RequireWithWriter(feature Feature, writer io.Writer) error {
	if writer == nil {
		writer = os.Stdout
	}

	enabled, err := IsEnabled(feature)
	if err != nil {
		return err
	}
	if enabled {
		if err := announceEnabled(feature, writer); err != nil {
			return fmt.Errorf("writing preview feature notice: %w", err)
		}
		return nil
	}
	return exterrors.Compatibility(
		exterrors.CodeFeatureNotEnabled,
		fmt.Sprintf("preview feature %q is disabled", feature.ID),
		fmt.Sprintf(
			"enable by setting %s=true in your shell or CI before running azd",
			feature.EnvVar,
		),
	)
}

func announceEnabled(feature Feature, writer io.Writer) error {
	enabledNotices.Lock()
	defer enabledNotices.Unlock()

	if _, announced := enabledNotices.seen[feature.ID]; announced {
		return nil
	}
	if _, err := fmt.Fprintf(
		writer,
		"%s %s (%s=true). Preview behavior may change.\n",
		output.WithWarningFormat("(!) Preview feature enabled:"),
		feature.Description,
		feature.EnvVar,
	); err != nil {
		return err
	}
	enabledNotices.seen[feature.ID] = struct{}{}
	return nil
}
