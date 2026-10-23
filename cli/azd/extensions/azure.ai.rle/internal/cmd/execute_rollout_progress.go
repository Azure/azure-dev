// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"azure.ai.rle/internal/rollouts"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type executeRolloutProgress struct {
	Message             string                         `json:"message"`
	Phase               string                         `json:"phase"`
	Status              string                         `json:"status"`
	Sequence            int64                          `json:"sequence"`
	ElapsedMilliseconds int64                          `json:"elapsed_ms"`
	PartialResponse     *executeRolloutPartialResponse `json:"partial_response,omitempty"`
}

type executeRolloutPartialResponse struct {
	RolloutID string                        `json:"rollout_id"`
	Reward    *float64                      `json:"reward,omitempty"`
	Success   *bool                         `json:"success,omitempty"`
	Episode   *executeRolloutPartialEpisode `json:"episode,omitempty"`
}

type executeRolloutPartialEpisode struct {
	Kind              string          `json:"kind"`
	Steps             []rollouts.Step `json:"steps"`
	TerminationReason *string         `json:"termination_reason,omitempty"`
	Ungraded          *bool           `json:"ungraded,omitempty"`
}

func decodeExecuteRolloutProgress(
	payload []byte,
	rolloutID string,
	previousSequence int64,
) (executeRolloutProgress, error) {
	var required struct {
		Message             *string         `json:"message"`
		Phase               *string         `json:"phase"`
		Status              *string         `json:"status"`
		Sequence            *int64          `json:"sequence"`
		ElapsedMilliseconds *int64          `json:"elapsed_ms"`
		PartialResponse     json.RawMessage `json:"partial_response"`
	}
	if err := json.Unmarshal(payload, &required); err != nil {
		return executeRolloutProgress{}, fmt.Errorf("decode Execute Rollout progress: %w", err)
	}
	if required.Message == nil || strings.TrimSpace(*required.Message) == "" {
		return executeRolloutProgress{}, fmt.Errorf("decode Execute Rollout progress: message is required")
	}
	if required.Phase == nil || strings.TrimSpace(*required.Phase) == "" {
		return executeRolloutProgress{}, fmt.Errorf("decode Execute Rollout progress: phase is required")
	}
	if required.Status == nil || strings.TrimSpace(*required.Status) == "" {
		return executeRolloutProgress{}, fmt.Errorf("decode Execute Rollout progress: status is required")
	}
	if required.Sequence == nil || *required.Sequence <= previousSequence {
		return executeRolloutProgress{}, fmt.Errorf(
			"decode Execute Rollout progress: sequence must increase beyond %d",
			previousSequence,
		)
	}
	if required.ElapsedMilliseconds == nil || *required.ElapsedMilliseconds < 0 {
		return executeRolloutProgress{}, fmt.Errorf(
			"decode Execute Rollout progress: elapsed_ms must be a nonnegative integer",
		)
	}

	progress := executeRolloutProgress{
		Message:             *required.Message,
		Phase:               strings.TrimSpace(*required.Phase),
		Status:              strings.TrimSpace(*required.Status),
		Sequence:            *required.Sequence,
		ElapsedMilliseconds: *required.ElapsedMilliseconds,
	}
	if len(required.PartialResponse) == 0 || string(required.PartialResponse) == "null" {
		return progress, nil
	}
	if err := json.Unmarshal(required.PartialResponse, &progress.PartialResponse); err != nil {
		return executeRolloutProgress{}, fmt.Errorf("decode Execute Rollout partial response: %w", err)
	}
	if progress.PartialResponse == nil || progress.PartialResponse.RolloutID != rolloutID {
		return executeRolloutProgress{}, fmt.Errorf(
			"decode Execute Rollout progress: partial_response rollout_id does not match the frame",
		)
	}
	if episode := progress.PartialResponse.Episode; episode != nil {
		if strings.TrimSpace(episode.Kind) == "" {
			return executeRolloutProgress{}, fmt.Errorf(
				"decode Execute Rollout progress: partial_response episode kind is required",
			)
		}
		if episode.Steps == nil {
			return executeRolloutProgress{}, fmt.Errorf(
				"decode Execute Rollout progress: partial_response episode steps are required",
			)
		}
	}
	return progress, nil
}

type executeRolloutProgressRenderer struct {
	out io.Writer
}

func newExecuteRolloutProgressRenderer(out io.Writer) *executeRolloutProgressRenderer {
	return &executeRolloutProgressRenderer{out: out}
}

func (r *executeRolloutProgressRenderer) Render(progress executeRolloutProgress) error {
	elapsed := output.WithGrayFormat("[%8s]", formatRolloutProgressElapsed(progress.ElapsedMilliseconds))
	prefix := rolloutProgressPrefix(progress.Phase, progress.Status)
	if _, err := fmt.Fprintf(
		r.out,
		"  %s %s %s\n",
		elapsed,
		prefix,
		sanitizeRolloutProgressMessage(progress.Message),
	); err != nil {
		return err
	}
	if summary := summarizeRolloutProgress(progress); summary != "" {
		_, err := fmt.Fprintf(r.out, "             %s\n", output.WithGrayFormat("%s", summary))
		return err
	}
	return nil
}

func rolloutProgressPrefix(phase string, status string) string {
	switch strings.ToLower(status) {
	case "started":
		return output.WithGrayFormat("Working:")
	case "completed":
		return output.WithSuccessFormat("(✓) Done:")
	case "available":
		return output.WithSuccessFormat("(✓) Ready:")
	case "failed":
		return output.WithErrorFormat("(x) Failed:")
	default:
		return output.WithGrayFormat(
			"%s/%s:",
			sanitizeRolloutProgressMessage(phase),
			sanitizeRolloutProgressMessage(status),
		)
	}
}

func sanitizeRolloutProgressMessage(message string) string {
	return strings.Map(func(value rune) rune {
		if unicode.IsControl(value) {
			return ' '
		}
		return value
	}, strings.TrimSpace(message))
}

func formatRolloutProgressElapsed(milliseconds int64) string {
	if milliseconds > math.MaxInt64/int64(time.Millisecond) {
		return strconv.FormatInt(milliseconds, 10) + "ms"
	}
	elapsed := time.Duration(milliseconds) * time.Millisecond
	if elapsed < time.Second {
		return elapsed.String()
	}
	return elapsed.Round(100 * time.Millisecond).String()
}

func summarizeRolloutProgress(progress executeRolloutProgress) string {
	partial := progress.PartialResponse
	if partial == nil {
		return ""
	}
	if progress.Phase == "environment_step" {
		if partial.Episode == nil || len(partial.Episode.Steps) == 0 {
			return ""
		}
		latest := partial.Episode.Steps[len(partial.Episode.Steps)-1]
		if partial.Episode.TerminationReason == nil && !latest.EpisodeDone {
			return ""
		}
		parts := []string{"Episode ended"}
		if partial.Episode.TerminationReason != nil {
			parts[0] += ": " + *partial.Episode.TerminationReason
		}
		if partial.Episode.Ungraded != nil && *partial.Episode.Ungraded {
			parts = append(parts, "ungraded")
		}
		return strings.Join(parts, " · ")
	}

	parts := make([]string, 0, 5)
	if partial.Episode != nil {
		stepLabel := "steps"
		if len(partial.Episode.Steps) == 1 {
			stepLabel = "step"
		}
		parts = append(parts, fmt.Sprintf(
			"%d %s",
			len(partial.Episode.Steps),
			stepLabel,
		))
	}
	if partial.Reward != nil {
		parts = append(parts, "reward "+strconv.FormatFloat(*partial.Reward, 'g', -1, 64))
	}
	if partial.Success != nil {
		parts = append(parts, "success "+strconv.FormatBool(*partial.Success))
	}
	if partial.Episode != nil && partial.Episode.TerminationReason != nil {
		parts = append(parts, "termination "+*partial.Episode.TerminationReason)
	}
	if partial.Episode != nil && partial.Episode.Ungraded != nil && *partial.Episode.Ungraded {
		parts = append(parts, "ungraded")
	}
	return strings.Join(parts, " · ")
}
