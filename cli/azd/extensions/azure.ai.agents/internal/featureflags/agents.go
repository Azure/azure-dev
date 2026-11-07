// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package featureflags

import (
	"fmt"
	"io"
	"os"
	"sync"

	"azure.ai.projects/synthesis"
	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

var enabledNotices = struct {
	sync.Mutex
	seen map[string]struct{}
}{seen: make(map[string]struct{})}

// ResetNoticesForTesting clears process notice state between tests.
func ResetNoticesForTesting() {
	enabledNotices.Lock()
	clear(enabledNotices.seen)
	enabledNotices.Unlock()
}

// Require announces an enabled preview once per process.
// Disabled features are rejected before their operation starts.
func Require(feature Feature) error {
	return RequireWithWriter(feature, os.Stdout)
}

// RequireWithWriter checks and announces a preview on writer.
func RequireWithWriter(feature Feature, writer io.Writer) error {
	if writer == nil {
		writer = os.Stdout
	}

	if err := Check(feature); err != nil {
		return err
	}
	if writer == io.Discard {
		return nil
	}
	if err := announceEnabled(feature, writer); err != nil {
		return fmt.Errorf("writing preview feature notice: %w", err)
	}
	return nil
}

// Check rejects a disabled feature without announcing enabled features.
func Check(feature Feature) error {
	if feature == FoundryACRReuse {
		return synthesis.RequireACRReuse()
	}
	enabled, err := IsEnabled(feature)
	if err != nil {
		return err
	}
	if !enabled {
		return disabledFeatureError(feature)
	}
	return nil
}

// RequireWithNotice returns an enabled feature notice once per process.
// The returned message is intended for host-visible lifecycle responses.
func RequireWithNotice(feature Feature) (string, error) {
	if err := Check(feature); err != nil {
		return "", err
	}

	enabledNotices.Lock()
	defer enabledNotices.Unlock()

	if _, announced := enabledNotices.seen[feature.ID]; announced {
		return "", nil
	}
	enabledNotices.seen[feature.ID] = struct{}{}
	return noticeText(feature), nil
}

func disabledFeatureError(feature Feature) error {
	return exterrors.Compatibility(
		exterrors.CodeFeatureNotEnabled,
		fmt.Sprintf("preview feature %q is disabled", feature.ID),
		fmt.Sprintf(
			"enable by setting %s=true in your shell or CI before running azd",
			feature.EnvVar,
		),
	)
}

func noticeText(feature Feature) string {
	return fmt.Sprintf(
		"Preview feature enabled: %s (%s=true). Preview behavior may change.",
		feature.Description,
		feature.EnvVar,
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
