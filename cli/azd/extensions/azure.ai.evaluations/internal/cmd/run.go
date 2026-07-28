// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/spf13/cobra"
)

// Terminal run states reported by the service.
var terminalRunStates = map[string]bool{
	"completed": true,
	"failed":    true,
	"canceled":  true,
	"cancelled": true,
	"error":     true,
}

// newRunCommand builds the composite `azd ai eval run` and attaches the atomic
// run operations, including `run start` which the spec lists as the atomic form
// of this same command.
func newRunCommand() *cobra.Command {
	cmd := buildRunCommand(
		"run", "Run an evaluation, creating the eval group if it does not exist yet.")
	addRunSubcommands(cmd)
	cmd.AddCommand(buildRunCommand(
		"start", "Start a run, creating the eval group if it does not exist yet."))
	return cmd
}

// buildRunCommand is shared by `run` and `run start` so the two forms cannot
// drift apart.
func buildRunCommand(use, short string) *cobra.Command {
	var (
		configPath  string
		groupName   string
		evalID      string
		runName     string
		level       string
		maxSamples  int
		fromTraces  bool
		traceWindow string
		maxTraces   int
		wait        bool
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			// --eval-id bypasses the config entirely.
			var group *project.EvalGroup
			if evalID == "" {
				cfg, err := project.LoadEvalConfig(configPath)
				if err != nil {
					return err
				}
				if err := cfg.Validate(); err != nil {
					return err
				}
				group, err = cfg.ResolveGroup(groupName)
				if err != nil {
					return err
				}

				if err := ec.checkDatasetRegistered(ctx, cfg, group, configPath); err != nil {
					return err
				}

				evalID, err = ec.resolveEvalGroupID(
					ctx, group, configPath, resolveLevel(level, group), out, isJSON(cmd))
				if err != nil {
					return err
				}
			}

			// With --eval-id there is no config to read, so the pairing of
			// target and dataset comes from the group's previous run.
			var dataSource *eval_api.EvalRunDataSource
			switch {
			case fromTraces:
				dataSource, err = buildTracesDataSource(ctx, ec, group, evalID, traceWindow, maxTraces)
			case group == nil:
				dataSource, err = ec.reuseDataSourceFromLastRun(ctx, evalID)
			default:
				dataSource, err = buildRunDataSource(
					group, configPath, resolveMaxSamples(maxSamples, group))
			}
			if err != nil {
				return err
			}

			if runName == "" {
				base := "eval"
				if group != nil {
					base = group.Name
				}
				runName = fmt.Sprintf("%s-%s", base, time.Now().UTC().Format("20060102-150405"))
			}

			metadata := map[string]string{}
			if lvl := resolveLevel(level, group); lvl != "" {
				metadata["evaluation_level"] = lvl
			}

			run, err := ec.evalClient.CreateOpenAIEvalRun(ctx, evalID, &eval_api.CreateOpenAIEvalRunRequest{
				Name:       runName,
				DataSource: dataSource,
				Metadata:   metadata,
			})
			if err != nil {
				return fmt.Errorf("starting the evaluation run: %w", err)
			}

			if err := ec.setEnvValue(ctx, envKeyEvalRunID, run.ID); err != nil {
				// Persisting the run id is a convenience for later commands.
				// Reported on stdout because azd does not surface an
				// extension's stderr, and skipped outside a project.
				if !errors.Is(err, errNoAzdEnvironment) && !isJSON(cmd) {
					fmt.Fprintf(out, "warning: %v\n", err)
				}
			}

			if !wait {
				if isJSON(cmd) {
					return emitJSON(out, run)
				}
				fmt.Fprintf(out, "Started run %s (status: %s)\n", run.ID, run.Status)
				fmt.Fprintf(out, "Check progress with: azd ai eval results show %s --run-id %s\n", evalID, run.ID)
				return nil
			}

			final, err := ec.pollRun(ctx, evalID, run.ID, out, isJSON(cmd))
			if err != nil {
				return err
			}

			if isJSON(cmd) {
				return emitJSON(out, final)
			}
			return renderRun(out, final)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", project.DefaultDeployConfig,
		"Path to the eval deployment config.")
	cmd.Flags().StringVar(&groupName, "eval-group", "",
		"Which evalGroups entry to run. Defaults to the only one.")
	cmd.Flags().StringVar(&evalID, "eval-id", "",
		"Run against an existing eval group by id, ignoring the config.")
	cmd.Flags().StringVar(&runName, "name", "", "Name for this run. Defaults to the group name plus a timestamp.")
	cmd.Flags().StringVar(&level, "level", "",
		"Scoring granularity: turn or conversation. Defaults to the service default (turn).")
	cmd.Flags().IntVar(&maxSamples, "max-samples", 0,
		"Cap the rows sent from a local dataset file. Ignored for registered datasets.")
	cmd.Flags().BoolVar(&fromTraces, "from-traces", false,
		"Evaluate the agent's recorded traces instead of the dataset.")
	cmd.Flags().StringVar(&traceWindow, "trace-window", "",
		"How far back to read traces, for example 7d. Defaults to the service's window.")
	cmd.Flags().IntVar(&maxTraces, "max-traces", 0, "Cap the traces evaluated.")
	cmd.Flags().BoolVar(&wait, "wait", true, "Block until the run reaches a terminal state.")
	// The spec documents --no-wait, and cobra does not derive it from a bool.
	var noWait bool
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Submit the run and return immediately.")
	cmd.PreRun = func(*cobra.Command, []string) {
		if noWait {
			wait = false
		}
	}
	cmd.MarkFlagsMutuallyExclusive("wait", "no-wait")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")

	return cmd
}

// resolveEvalGroupID finds the eval group to run against, creating it when it
// has never been deployed. Resolution order: an id pinned on the group, then
// the azd environment, then create.
func (ec *evalContext) resolveEvalGroupID(
	ctx context.Context,
	group *project.EvalGroup,
	configPath string,
	level string,
	out interface{ Write([]byte) (int, error) },
	jsonMode bool,
) (string, error) {
	if group.ID != "" {
		return group.ID, nil
	}

	if cached := ec.getEnvValue(ctx, envKeyEvalGroupID); cached != "" {
		// Confirm it still exists; a deleted group should fall through to create.
		if _, err := ec.evalClient.GetOpenAIEval(ctx, cached); err == nil {
			return cached, nil
		}
	}

	if !jsonMode {
		fmt.Fprintf(out, "Creating eval group %q...\n", group.Name)
	}

	// The level from the flag wins over the group's own options, so it has to
	// reach the criteria that accept evaluation_level.
	effective := *group
	if level != "" {
		opts := project.Options{}
		if group.Options != nil {
			opts = *group.Options
		}
		opts.EvaluationLevel = level
		effective.Options = &opts
	}

	req, err := buildEvalGroupRequest(
		&effective,
		ec.evaluatorSchemas(ctx),
		datasetColumns(configPath, group),
	)
	if err != nil {
		return "", err
	}
	created, err := ec.evalClient.CreateOpenAIEval(ctx, req)
	if err != nil {
		return "", fmt.Errorf("creating eval group %q: %w", group.Name, err)
	}
	if err := ec.setEnvValue(ctx, envKeyEvalGroupID, created.ID); err != nil {
		fmt.Fprintf(out, "warning: %v\n", err)
	}
	return created.ID, nil
}

// checkDatasetRegistered fails when the group's local dataset has edits that
// were never deployed.
//
// A run sends a local dataset inline, so without this the run would evaluate
// content that no registered version corresponds to: the results are attributed
// to the eval group but cannot be traced back to a dataset version, which
// makes them impossible to reproduce or compare.
//
// The check only applies once a deploy has recorded a fingerprint. Before that
// there is nothing to have drifted from, and running is how a group first comes
// into existence.
func (ec *evalContext) checkDatasetRegistered(
	ctx context.Context,
	cfg *project.EvalConfig,
	group *project.EvalGroup,
	configPath string,
) error {
	localPath := localDatasetPath(configPath, group)
	if localPath == "" {
		return nil
	}

	decl, ok := cfg.Dataset(group.Dataset)
	if !ok {
		return nil
	}

	recorded := ec.getEnvValue(ctx, project.FingerprintKey("dataset", decl.Name))
	if recorded == "" {
		return nil
	}

	digest, err := project.Fingerprint(localPath)
	if err != nil {
		// Reading the file is the run's problem to report, not this check's.
		return nil
	}
	if digest == recorded {
		return nil
	}

	return fmt.Errorf(
		"dataset %q has local edits that are not registered.\n"+
			"  Run `azd up` to register them, or `--eval-id <id>` to run against "+
			"an existing eval group",
		decl.Name)
}

// reuseDataSourceFromLastRun rebuilds a run's data source from the group's most
// recent run.
//
// `--eval-id` deliberately ignores the config, but a run still needs a target
// and a dataset, and an eval group carries neither: the group holds only its
// testing criteria, and the dataset travels on the run. The previous run is the
// only place that pairing survives, so re-running a group means repeating what
// it last ran.
func (ec *evalContext) reuseDataSourceFromLastRun(
	ctx context.Context,
	evalID string,
) (*eval_api.EvalRunDataSource, error) {
	list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, 1)
	if err != nil {
		return nil, fmt.Errorf("reading previous runs of eval group %s: %w", evalID, err)
	}
	if list == nil || len(list.Data) == 0 || list.Data[0].DataSource == nil {
		return nil, fmt.Errorf(
			"eval group %s has no previous run to repeat, so there is no target or dataset "+
				"to reuse.\n"+
				"  Run it from the config once with `azd ai eval run`, or pass a config that "+
				"declares the group",
			evalID)
	}
	return list.Data[0].DataSource, nil
}

// buildTracesDataSource evaluates what the agent has already done, rather than
// asking it fresh questions from a dataset.
//
// The service reads the traces from Application Insights, so the agent has to
// be emitting gen_ai.input.messages / gen_ai.output.messages for anything to be
// found; when it is not, the run fails with the service saying so.
func buildTracesDataSource(
	ctx context.Context,
	ec *evalContext,
	group *project.EvalGroup,
	evalID, window string,
	maxTraces int,
) (*eval_api.EvalRunDataSource, error) {
	agent := ""
	switch {
	case group != nil && group.Target != nil:
		agent = group.Target.Name
	default:
		// With --eval-id there is no config, so the agent comes from whatever
		// the group ran against last.
		last, err := ec.reuseDataSourceFromLastRun(ctx, evalID)
		if err != nil {
			return nil, err
		}
		if last != nil && last.Target != nil {
			agent = last.Target.Name
		}
	}
	if agent == "" {
		return nil, fmt.Errorf(
			"--from-traces needs to know whose traces to read, and the eval group does not " +
				"name an agent. Declare target.type: agent on the group")
	}

	var start, end time.Time
	if days := parseWindowDays(window); days > 0 {
		end = time.Now().UTC()
		start = end.AddDate(0, 0, -days)
	}
	return eval_api.NewTracesDataSource(agent, start, end, maxTraces), nil
}

// buildRunDataSource binds the dataset to the run. The eval group carries no
// dataset today, so it is supplied here.
func buildRunDataSource(
	group *project.EvalGroup,
	configPath string,
	maxSamples int,
) (*eval_api.EvalRunDataSource, error) {
	if group == nil || group.Target == nil {
		return nil, fmt.Errorf(
			"the eval group must declare target.type: agent so the run knows what to invoke")
	}

	ds := eval_api.NewAgentTargetDataSource(group.Target.Name, nil)

	if group.Dataset == "" {
		return nil, fmt.Errorf("eval group %q does not reference a dataset", group.Name)
	}

	// A local source is sent inline; anything else is a registered dataset.
	localPath := localDatasetPath(configPath, group)
	if localPath == "" {
		ds.SetFileID(group.Dataset)
		return ds, nil
	}

	items, err := readJSONL(localPath, maxSamples)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("dataset file %q has no rows", localPath)
	}
	ds.SetFileContent(items)
	return ds, nil
}

// datasetColumns reports the columns a group's dataset provides, so criteria
// bind only to fields that exist and a missing required field is caught
// locally rather than as a service rejection.
//
// A nil result means the columns are unknown, which is the case for a dataset
// already registered in the project. The builder then assumes every field an
// evaluator accepts is present.
func datasetColumns(configPath string, group *project.EvalGroup) map[string]bool {
	return datasetColumnsFromPath(localDatasetPath(configPath, group))
}

// datasetColumnsFromPath reads one row to learn the dataset's shape. An empty
// path, or an unreadable file, yields nil.
func datasetColumnsFromPath(localPath string) map[string]bool {
	if localPath == "" {
		return nil
	}
	// One row is enough to learn the shape.
	items, err := readJSONL(localPath, 1)
	if err != nil || len(items) == 0 {
		return nil
	}
	columns := make(map[string]bool, len(items[0]))
	for name := range items[0] {
		columns[name] = true
	}
	return columns
}

// localDatasetPath resolves the dataset's local source relative to the config
// file, returning empty when the dataset is registered rather than local.
func localDatasetPath(configPath string, group *project.EvalGroup) string {
	cfg, err := project.LoadEvalConfig(configPath)
	if err != nil {
		return ""
	}
	decl, ok := cfg.Dataset(group.Dataset)
	if !ok || decl.Source == "" {
		return ""
	}
	if filepath.IsAbs(decl.Source) {
		return decl.Source
	}
	return filepath.Join(filepath.Dir(configPath), decl.Source)
}

// readJSONL reads newline-delimited JSON, optionally truncating to limit rows.
func readJSONL(path string, limit int) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading dataset %q: %w", path, err)
	}
	defer f.Close()

	var items []map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(text), &row); err != nil {
			return nil, fmt.Errorf("%s line %d is not valid JSON: %w", path, line, err)
		}
		items = append(items, row)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading dataset %q: %w", path, err)
	}
	return items, nil
}

// resolveLevel prefers the flag, then the group's options.
func resolveLevel(flag string, group *project.EvalGroup) string {
	if flag != "" {
		return flag
	}
	if group != nil && group.Options != nil {
		return group.Options.EvaluationLevel
	}
	return ""
}

// resolveMaxSamples prefers the flag, then the group's options, matching how
// the evaluation level resolves.
//
// Without this, options.max_samples parsed and did nothing: a group that caps
// its sample count in config would send the whole dataset, and only a flag on
// every invocation would honour the cap.
func resolveMaxSamples(flag int, group *project.EvalGroup) int {
	if flag > 0 {
		return flag
	}
	if group != nil && group.Options != nil && group.Options.MaxSamples > 0 {
		return group.Options.MaxSamples
	}
	return 0
}

// pollRun waits for the run to reach a terminal state, reporting status changes.
func (ec *evalContext) pollRun(
	ctx context.Context,
	evalID, runID string,
	out interface{ Write([]byte) (int, error) },
	jsonMode bool,
) (*eval_api.OpenAIEvalRun, error) {
	const interval = 5 * time.Second
	lastStatus := ""

	for {
		run, err := ec.evalClient.GetOpenAIEvalRun(ctx, evalID, runID)
		if err != nil {
			return nil, fmt.Errorf("polling run %s: %w", runID, err)
		}
		if run.Status != lastStatus {
			lastStatus = run.Status
			if !jsonMode {
				fmt.Fprintf(out, "  status: %s\n", run.Status)
			}
		}
		if terminalRunStates[strings.ToLower(run.Status)] {
			return run, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func renderRun(out interface{ Write([]byte) (int, error) }, run *eval_api.OpenAIEvalRun) error {
	fmt.Fprintf(out, "\nRun %s finished with status %s\n", run.ID, run.Status)
	// A run that failed carries why, and it is usually the only actionable
	// thing in the response — dropping it leaves the caller with just the word
	// "failed".
	if why := run.Failure(); why != "" {
		fmt.Fprintf(out, "  %s\n", why)
	}
	if run.ReportURL != "" {
		fmt.Fprintf(out, "Report: %s\n", run.ReportURL)
	}
	return nil
}
