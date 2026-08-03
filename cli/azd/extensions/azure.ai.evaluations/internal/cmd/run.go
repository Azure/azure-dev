// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"azureaieval/internal/pkg/dataset_api"
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
// newRunCommand builds the run group.
//
// `run` is a group, not an executable verb: once `run output` exists, a bare
// `run` would make `azd ai eval run list` read as "run the thing called list".
func newRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start and inspect evaluation runs.",
	}
	addRunSubcommands(cmd)
	cmd.AddCommand(buildRunCommand(
		"start", "Start a run, creating the eval if it does not exist yet."))
	return cmd
}

// buildRunCommand builds `run start`.
func buildRunCommand(use, short string) *cobra.Command {
	var (
		groupName   string
		evalID      string
		runName     string
		level       string
		maxSamples  int
		wait        bool
		failOn      string
		endpointFlg string
	)

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			// Parsed before any network work, so a malformed threshold costs
			// nothing to find out about.
			threshold, err := parseGate(failOn)
			if err != nil {
				return err
			}

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			// --eval-id bypasses the config entirely.
			var group *project.Eval
			configPath := ""
			if evalID == "" {
				configPath, err = project.ResolveEvalConfigPath(project.DefaultEvalDir, groupName)
				if err != nil {
					return err
				}
				cfg, err := project.LoadEvalConfig(configPath)
				if err != nil {
					return err
				}
				if err := cfg.Validate(); err != nil {
					return err
				}
				resolved := cfg.Eval(evalNameFromPath(configPath))
				group = &resolved

				if err := ec.checkDatasetRegistered(ctx, cfg, group, configPath); err != nil {
					return err
				}

				evalID, err = ec.resolveEvalIDFromConfig(
					ctx, group, configPath, resolveLevel(level, group),
					out, isJSON(cmd))
				if err != nil {
					return err
				}
			}

			// With --eval-id there is no config to read, so the pairing of
			// target and dataset comes from the group's previous run.
			var dataSource *eval_api.EvalRunDataSource
			switch {
			case group == nil:
				dataSource, err = ec.reuseDataSourceFromLastRun(ctx, evalID)
			default:
				dataSource, err = ec.buildRunDataSource(
					ctx, group, configPath, resolveMaxSamples(maxSamples, group))
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

			// Remembered per group as well as globally: a single shared key
			// belongs to whichever group ran last, so another group asking for
			// "the last run" would be handed one that is not its own.
			_ = ec.setEnvValue(ctx, idKey("evalrun", evalID), run.ID)
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
				if err := emitJSON(out, final); err != nil {
					return err
				}
			} else if err := renderRun(out, final); err != nil {
				return err
			}

			// Last, so that the results are reported whether or not the gate
			// holds: a pipeline that only learns it failed is worse off than
			// one that can see by how much.
			applyGate(cmd, threshold, final)
			return nil
		},
	}

	cmd.Flags().StringVar(&groupName, "eval", "",
		"Name of the eval to run. Defaults to the only one declared.")
	cmd.Flags().StringVar(&evalID, "eval-id", "",
		"Run against an existing eval by id, ignoring the config.")
	cmd.Flags().StringVar(&runName, "name", "", "Name for this run. Defaults to the eval name plus a timestamp.")
	cmd.Flags().StringVar(&level, "level", "",
		"Scoring granularity: turn or conversation. Defaults to the service default (turn).")
	cmd.Flags().IntVar(&maxSamples, "max-samples", 0,
		"Cap the rows sent from the dataset.")
	cmd.Flags().BoolVar(&wait, "wait", true, "Block until the run reaches a terminal state.")
	addFailOnFlag(cmd, &failOn)
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

// resolveEvalIDFromConfig finds the eval to run against, creating it when it
// has never been deployed. Resolution order: an id pinned on the group, then
// the azd environment, then create.
func (ec *evalContext) resolveEvalIDFromConfig(
	ctx context.Context,
	group *project.Eval,
	configPath string,
	level string,
	out interface{ Write([]byte) (int, error) },
	jsonMode bool,
) (string, error) {
	if group.ID != "" {
		return group.ID, nil
	}

	for _, key := range evalIDKeys(group.Name, filepath.Dir(configPath)) {
		cached := ec.getEnvValue(ctx, key)
		if cached == "" {
			continue
		}
		// Confirm it still exists; a deleted group should fall through to create.
		if _, err := ec.evalClient.GetOpenAIEval(ctx, cached); err == nil {
			return cached, nil
		}
	}

	if !jsonMode {
		fmt.Fprintf(out, "Creating eval %q...\n", group.Name)
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

	req, err := buildEvalRequest(
		&effective,
		ec.evaluatorSchemas(ctx),
		datasetColumns(configPath, group),
	)
	if err != nil {
		return "", err
	}
	created, err := ec.evalClient.CreateOpenAIEval(ctx, req)
	if err != nil {
		return "", fmt.Errorf("creating eval %q: %w", group.Name, err)
	}
	if err := ec.setEnvValue(ctx, idKey("eval", group.Name), created.ID); err != nil {
		fmt.Fprintf(out, "warning: %v\n", err)
	}
	_ = ec.setEnvValue(ctx, envKeyEvalID, created.ID)
	return created.ID, nil
}

// evalIDKeys lists the env entries that may hold this eval's id, most
// specific first.
//
// The per-name entry is what the extension writes. EVAL_ID is also the
// documented way to point a config at an eval that already exists, created in
// the portal or by another tool, so it stays readable — but only when the
// project declares a single eval. With more than one there is no way to tell
// which eval a shared entry refers to, and reading it anyway is what let a
// second eval adopt the first one's id.
func evalIDKeys(name, evalDir string) []string {
	keys := []string{idKey("eval", name)}
	if names, err := project.EvalNamesIn(evalDir); err == nil && len(names) == 1 {
		keys = append(keys, envKeyEvalID)
	}
	return keys
}

// checkDatasetRegistered fails when the group's local dataset has edits that
// were never deployed.
//
// A run sends a local dataset inline, so without this the run would evaluate
// content that no registered version corresponds to: the results are attributed
// to the eval but cannot be traced back to a dataset version, which
// makes them impossible to reproduce or compare.
//
// The check only applies once a deploy has recorded a fingerprint. Before that
// there is nothing to have drifted from, and running is how a group first comes
// into existence.
func (ec *evalContext) checkDatasetRegistered(
	ctx context.Context,
	cfg *project.EvalConfig,
	group *project.Eval,
	configPath string,
) error {
	localPath := localDatasetPath(configPath, group)
	if localPath == "" {
		return nil
	}

	decl := cfg.Dataset
	if decl == nil {
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
			"an existing eval",
		decl.Name)
}

// reuseDataSourceFromLastRun rebuilds a run's data source from the group's most
// recent run.
//
// `--eval-id` deliberately ignores the config, but a run still needs a target
// and a dataset, and an eval carries neither: the group holds only its
// testing criteria, and the dataset travels on the run. The previous run is the
// only place that pairing survives, so re-running a group means repeating what
// it last ran.
func (ec *evalContext) reuseDataSourceFromLastRun(
	ctx context.Context,
	evalID string,
) (*eval_api.EvalRunDataSource, error) {
	list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, 1)
	if err != nil {
		return nil, fmt.Errorf("reading previous runs of eval %s: %w", evalID, err)
	}
	if list == nil || len(list.Data) == 0 || list.Data[0].DataSource == nil {
		return nil, fmt.Errorf(
			"eval %s has no previous run to repeat, so there is no target or dataset "+
				"to reuse.\n"+
				"  Run it from the config once with `azd ai eval run`, or pass a config that "+
				"declares the eval",
			evalID)
	}
	return list.Data[0].DataSource, nil
}

// buildRunDataSource binds the dataset to the run. The eval carries no
// dataset today, so it is supplied here.
func (ec *evalContext) buildRunDataSource(
	ctx context.Context,
	group *project.Eval,
	configPath string,
	maxSamples int,
) (*eval_api.EvalRunDataSource, error) {
	if group == nil {
		return nil, fmt.Errorf("no eval to run")
	}
	if group.Target == nil || group.Target.Name == "" {
		return nil, fmt.Errorf("eval %q does not name a target agent", group.Name)
	}

	ds := eval_api.NewAgentTargetDataSource(group.Target.Name, nil)

	if group.Dataset == "" {
		return nil, fmt.Errorf("eval %q does not reference a dataset", group.Name)
	}

	// A local source is read from disk; anything else is already registered and
	// has to be fetched. Either way the rows are sent inline, because a run's
	// file_id means an uploaded file and a dataset name is not one: sending the
	// name is rejected with "invalid data source file ids".
	localPath := localDatasetPath(configPath, group)
	if localPath == "" {
		items, err := ec.readRegisteredDataset(ctx, group.Dataset, maxSamples)
		if err != nil {
			return nil, err
		}
		ds.SetFileContent(items)
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

// readRegisteredDataset fetches a published dataset's rows, optionally keeping
// only the first n.
//
// The rows have to be fetched because a run cannot reference a dataset by
// name: `file_id` means an uploaded file, and passing a dataset name there is
// rejected. Fetching also makes --max-samples mean the same thing whether the
// dataset is local or published, which a file reference could not — that
// source carries no row limit.
func (ec *evalContext) readRegisteredDataset(
	ctx context.Context,
	name string,
	maxSamples int,
) ([]map[string]any, error) {
	version := ec.getEnvValue(ctx, versionKey("dataset", name))
	if version == "" {
		versions, err := ec.datasetClient.ListDatasetVersions(ctx, name, ProjectEndpointAPIVersion)
		if err != nil {
			return nil, fmt.Errorf("reading dataset %q: %w", name, err)
		}
		if versions != nil {
			version = dataset_api.LatestVersion(versions.Value)
		}
	}
	if version == "" {
		return nil, fmt.Errorf("dataset %q has no versions to read", name)
	}

	content, err := ec.datasetClient.DownloadDatasetContent(
		ctx, name, version, ProjectEndpointAPIVersion)
	if err != nil {
		return nil, fmt.Errorf("reading dataset %q version %s: %w", name, version, err)
	}

	items, err := readJSONLBytes(content, maxSamples)
	if err != nil {
		return nil, fmt.Errorf("reading dataset %q version %s: %w", name, version, err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("dataset %q version %s has no rows", name, version)
	}
	return items, nil
}

// datasetColumns reports the columns a group's dataset provides, so criteria
// bind only to fields that exist and a missing required field is caught
// locally rather than as a service rejection.
//
// A nil result means the columns are unknown, which is the case for a dataset
// already registered in the project. The builder then assumes every field an
// evaluator accepts is present.
func datasetColumns(configPath string, group *project.Eval) map[string]bool {
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
func localDatasetPath(configPath string, group *project.Eval) string {
	cfg, err := project.LoadEvalConfig(configPath)
	if err != nil {
		return ""
	}
	decl := cfg.Dataset
	if decl == nil || decl.Source == "" {
		return ""
	}
	if filepath.IsAbs(decl.Source) {
		return decl.Source
	}
	return filepath.Join(filepath.Dir(configPath), decl.Source)
}

// evalNameFromPath is the eval's name: one config file is one eval, and the
// file is named after it, matching the azure.yaml service key that $refs it.
func evalNameFromPath(configPath string) string {
	base := filepath.Base(configPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// readJSONL reads newline-delimited JSON, optionally truncating to limit rows.
func readJSONL(path string, limit int) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading dataset %q: %w", path, err)
	}
	defer f.Close()

	items, err := scanJSONL(f, limit)
	if err != nil {
		return nil, fmt.Errorf("reading dataset %q: %w", path, err)
	}
	return items, nil
}

// readJSONLBytes parses JSONL already in memory, which is how a registered
// dataset arrives.
func readJSONLBytes(content []byte, limit int) ([]map[string]any, error) {
	return scanJSONL(bytes.NewReader(content), limit)
}

// scanJSONL reads rows until the limit is reached, so a subset costs only the
// rows it needs to parse.
func scanJSONL(r io.Reader, limit int) ([]map[string]any, error) {
	var items []map[string]any
	scanner := bufio.NewScanner(r)
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
			return nil, fmt.Errorf("line %d is not valid JSON: %w", line, err)
		}
		items = append(items, row)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// resolveLevel prefers the flag, then the group's options.
func resolveLevel(flag string, group *project.Eval) string {
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
func resolveMaxSamples(flag int, group *project.Eval) int {
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
