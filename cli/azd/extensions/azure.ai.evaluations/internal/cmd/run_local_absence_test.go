// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunStartDatasetOverridePreservesAttributionAndExplicitZero(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "azure.eval.yaml")
	config, err := json.Marshal(project.EvalConfig{
		Datasets: []project.DatasetDecl{
			{Name: "original", File: "original.jsonl"},
			{Name: "golden", File: "golden.jsonl"},
		},
		Evals: []project.Eval{{Name: "quality", Dataset: "original", MaxSamples: 1}},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, config, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "original.jsonl"),
		[]byte("{\"query\":\"original row\"}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "golden.jsonl"),
		[]byte("{\"query\":\"override first\"}\n{\"query\":\"override second\"}\n"), 0o600))
	ec, requests := identityRunContext(t, identityService{
		listStatus: http.StatusNotFound, getStatus: http.StatusNotFound,
	})
	ec.state = map[string]string{idKey("eval", "quality"): "eval_1"}
	cmd := buildRunCommand("start", "")
	cmd.Flags().String("output", "json", "")
	require.NoError(t, cmd.Flags().Set("dataset", "golden"))
	require.NoError(t, cmd.Flags().Set("max-samples", "0"))
	dataset, err := cmd.Flags().GetString("dataset")
	require.NoError(t, err)
	cap, err := cmd.Flags().GetInt("max-samples")
	require.NoError(t, err)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	action := &runStartAction{cmd: cmd, flags: &runStartFlags{
		groupName: "quality", evalPath: dir, datasetName: dataset, maxSamples: cap, wait: false,
	}}
	require.NoError(t, action.start(t.Context(), ec, gate{}))

	recorded := recordedIdentityRequests(requests)
	require.Len(t, recorded, 4, "only absence lookup and one run submission; no publication or original dataset lookup")
	for index, path := range []string{
		"/datasets/golden/versions", "/datasets/golden/versions/1.0", "/datasets/golden/versions/1",
	} {
		assert.Equal(t, http.MethodGet, recorded[index].method)
		assert.Equal(t, path, recorded[index].path)
	}
	assert.Equal(t, http.MethodPost, recorded[3].method)
	assert.Equal(t, "/openai/v1/evals/eval_1/runs", recorded[3].path)
	var submitted eval_api.CreateOpenAIEvalRunRequest
	require.NoError(t, json.Unmarshal(recorded[3].body, &submitted))
	assert.Equal(t, map[string]string{metaEvalName: "quality", metaDataset: "golden"}, submitted.Metadata)
	require.NotNil(t, submitted.DataSource)
	require.NotNil(t, submitted.DataSource.Source)
	assert.Equal(t, eval_api.EvalRunDataContentTypeFileContent, submitted.DataSource.Source.Type)
	assert.Empty(t, submitted.DataSource.Source.ID)
	assert.Equal(t, []map[string]any{
		{"query": "override first"}, {"query": "override second"},
	}, submitted.DataSource.Source.Content)
	var handoff map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &handoff))
	assert.Equal(t, map[string]any{
		"run_id": "evalrun_new", "eval_id": "eval_1", "eval_name": "quality", "dataset": "golden", "status": "queued",
	}, handoff)
	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, config, after, "a run override must not edit the declaration")
}

func TestLocalDatasetAbsenceSubmitsDeclaredAndOverriddenRows(t *testing.T) {
	for _, cap := range []int{0, 1} {
		for _, override := range []string{"", "golden"} {
			for _, target := range []string{"", project.TargetTypeAgent} {
				name := fmt.Sprintf("cap%d/override%s/target%s", cap, override, target)
				t.Run(name, func(t *testing.T) {
					ec, requests := identityRunContext(t, identityService{
						listStatus: http.StatusNotFound,
						getStatus:  http.StatusNotFound,
					})
					config := writeCatalog(t, "golden.jsonl", "")
					require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(config), "golden.jsonl"),
						[]byte(oneRow+`{"query":"second question","ground_truth":"second answer"}`+"\n"), 0o600))
					cfg, err := project.LoadEvalConfig(config)
					require.NoError(t, err)
					declared := &project.Eval{Name: "quality", Dataset: "golden"}
					if override != "" {
						declared.Dataset = "original"
					}
					if target != "" {
						declared.Target = &project.Target{Type: target, Name: "agent"}
					}
					cmd := buildRunCommand("start", "")
					require.NoError(t, cmd.Flags().Set("max-samples", fmt.Sprint(cap)))
					require.NoError(t, cmd.Flags().Set("dataset", override))
					datasetFlag, err := cmd.Flags().GetString("dataset")
					require.NoError(t, err)
					group, err := withRunDatasetOverride(evalRef{
						Eval: declared, Config: cfg, ConfigPath: config,
					}, datasetFlag)
					require.NoError(t, err)
					limit, err := runMaxSamples(cmd, cap, group)
					require.NoError(t, err)
					ds, version, err := ec.buildRunDataSource(t.Context(), group, config, limit)
					require.NoError(t, err)
					assert.Empty(t, version, "local rows must not claim a registered version")
					if override != "" {
						assert.Equal(t, "original", declared.Dataset, "an override must not edit the declaration")
					}
					submitIdentitySource(t, ec, ds, version, "")

					recorded := recordedIdentityRequests(requests)
					require.Len(t, recorded, 4, "only version lookup, two probes, and run submission are needed")
					assert.Equal(t, "/datasets/golden/versions", recorded[0].path)
					assert.Equal(t, "/datasets/golden/versions/1.0", recorded[1].path)
					assert.Equal(t, "/datasets/golden/versions/1", recorded[2].path)
					for _, request := range recorded[:3] {
						assert.Equal(t, http.MethodGet, request.method)
					}
					posted := recorded[3]
					assert.Equal(t, http.MethodPost, posted.method)
					assert.True(t, strings.HasSuffix(posted.path, "/runs"))
					var body map[string]any
					require.NoError(t, json.Unmarshal(posted.body, &body))
					source, ok := body["data_source"].(map[string]any)
					require.True(t, ok)
					content, ok := source["source"].(map[string]any)
					require.True(t, ok)
					assert.Equal(t, "file_content", content["type"])
					assert.NotContains(t, content, "id")
					wantRows := 2
					if cap > 0 {
						wantRows = cap
					}
					rows, ok := content["content"].([]any)
					require.True(t, ok)
					require.Len(t, rows, wantRows)
					assert.Equal(t, map[string]any{"query": "q", "ground_truth": "a"}, rows[0])
					if target == "" {
						assert.Equal(t, "jsonl", source["type"])
						assert.NotContains(t, source, "target")
					} else {
						assert.Equal(t, "azure_ai_target_completions", source["type"])
						assert.Contains(t, source, "target")
					}
				})
			}
		}
	}
}

func TestLocalDatasetAbsenceRequiresTypedNotFoundListing(t *testing.T) {
	for _, listStatus := range []int{0, http.StatusNotFound} {
		ec, _ := identityRunContext(t, identityService{listStatus: listStatus, getStatus: http.StatusNotFound})
		version, err := ec.lookupRunDatasetVersion(t.Context(), "golden")
		require.Error(t, err)
		assert.Empty(t, version)
		absent, ok := errors.AsType[*unregisteredDatasetError](err)
		if listStatus == http.StatusNotFound {
			require.True(t, ok)
			assert.Equal(t, "golden", absent.name)
		} else {
			assert.False(t, ok, "successful empty listings do not establish absence")
			assert.Contains(t, err.Error(), "registration is indeterminate")
			assert.Contains(t, err.Error(), "retry")
			assert.Contains(t, err.Error(), "declare a known dataset version")
		}
		assert.False(t, dataset_api.IsNotFound(err), "successful empty listings must not be relabeled HTTP 404")
	}
}

func TestEmptyDatasetListingBlocksRunsWithLaterVersions(t *testing.T) {
	for _, entry := range []string{"declared", "override", "rerun"} {
		for _, cap := range []int{0, 1} {
			if entry == "rerun" && cap != 0 {
				continue
			}
			t.Run(fmt.Sprintf("%s/cap%d", entry, cap), func(t *testing.T) {
				previous := eval_api.NewDatasetOnlyDataSource()
				previous.SetFileContent([]map[string]any{{"query": "old local row"}})
				ec, requests := identityRunContext(t, identityService{
					getStatus:   http.StatusNotFound,
					getStatuses: map[string]int{"7": http.StatusOK},
					id:          "issued-version-7",
					rows:        oneRow,
					previous: []*eval_api.OpenAIEvalRun{{
						ID: "old", DataSource: previous, Metadata: map[string]string{metaDataset: "golden"},
					}},
				})
				config := writeCatalog(t, "golden.jsonl", "")
				ec.state = map[string]string{idKey("eval", "quality"): "eval_1"}
				cmd := buildRunCommand("start", "")
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				flags := &runStartFlags{
					groupName: "quality", evalPath: filepath.Dir(config), maxSamples: cap, wait: false,
				}
				switch entry {
				case "override":
					cfg, err := project.LoadEvalConfig(config)
					require.NoError(t, err)
					cfg.Datasets = append(cfg.Datasets, project.DatasetDecl{Name: "original", File: "original.jsonl"})
					cfg.Evals[0].Dataset = "original"
					body, err := json.Marshal(cfg)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(config, body, 0o600))
					require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(config), "original.jsonl"),
						[]byte(oneRow), 0o600))
					flags.datasetName = "golden"
					require.NoError(t, cmd.Flags().Set("dataset", flags.datasetName))
				case "rerun":
					flags.groupName = "eval_1"
				}
				err := (&runStartAction{cmd: cmd, flags: flags}).start(t.Context(), ec, gate{})
				require.ErrorContains(t, err, "registration is indeterminate")
				_, absent := errors.AsType[*unregisteredDatasetError](err)
				assert.False(t, absent)
				recorded := recordedIdentityRequests(requests)
				wantPaths := []string{
					"/datasets/golden/versions", "/datasets/golden/versions/1.0", "/datasets/golden/versions/1",
				}
				if entry == "rerun" {
					wantPaths = append([]string{"/openai/v1/evals/eval_1/runs"}, wantPaths...)
				}
				require.Len(t, recorded, len(wantPaths))
				for i, request := range recorded {
					assert.Equal(t, http.MethodGet, request.method, "indeterminate presence must prevent submission")
					assert.Equal(t, wantPaths[i], request.path)
				}

				// Version 7 remains readable even though the listing is empty and
				// versions 1/1.0 are gone. An explicit pin preserves its identity.
				ds, version, err := ec.buildRunDataSource(t.Context(),
					&project.Eval{Name: "quality", Dataset: "golden"}, writeCatalog(t, "golden.jsonl", "7"), 0)
				require.NoError(t, err)
				assert.Equal(t, "7", version)
				submitIdentitySource(t, ec, ds, version, "")
				assert.Equal(t, map[string]any{"type": "file_id", "id": "issued-version-7"},
					identityPostedSource(t, requests)["source"])
			})
		}
	}
}

func TestLocalDatasetAbsenceRejectsMalformedListings(t *testing.T) {
	for _, body := range []string{
		"", "{", "null", "{}", `{"value":null}`, `{"value":{}}`, `{"value":[null]}`,
		`{"value":[{}]}`, `{"value":[{"version":""}]}`, `{"value":[{"version":" "}]}`,
		`{"value":[],"nextLink":17}`,
	} {
		t.Run(body, func(t *testing.T) {
			ec, requests := identityRunContext(t, identityService{listBody: new(body), getStatus: http.StatusNotFound})
			ds, _, err := ec.buildRunDataSource(t.Context(), &project.Eval{Name: "quality", Dataset: "golden"},
				writeCatalog(t, "golden.jsonl", ""), 1)
			require.Error(t, err)
			assert.Nil(t, ds)
			_, absent := errors.AsType[*unregisteredDatasetError](err)
			assert.False(t, absent)
			require.Len(t, recordedIdentityRequests(requests), 1, "malformed listing must stop before probes or submission")
		})
	}
}

func TestLocalDatasetAbsencePreservesLookupErrors(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500, 503} {
		for _, failedAt := range []string{"listing", "probe after empty list", "probe after not found"} {
			t.Run(fmt.Sprintf("%d/%s", status, failedAt), func(t *testing.T) {
				service := identityService{getStatus: status}
				switch failedAt {
				case "listing":
					service.listStatus = status
				case "probe after not found":
					service.listStatus = http.StatusNotFound
				}
				ec, requests := identityRunContext(t, service)
				ds, _, err := ec.buildRunDataSource(t.Context(), &project.Eval{Name: "quality", Dataset: "golden"},
					writeCatalog(t, "golden.jsonl", ""), 1)
				require.Error(t, err)
				assert.Nil(t, ds)
				_, absent := errors.AsType[*unregisteredDatasetError](err)
				assert.False(t, absent, "auth or service failure is not dataset absence")
				for _, request := range recordedIdentityRequests(requests) {
					assert.Equal(t, http.MethodGet, request.method)
				}
			})
		}
	}
}

func TestLocalDatasetAbsenceDoesNotBypassVersionBindings(t *testing.T) {
	for _, binding := range []string{"declared", "recorded", "no local file"} {
		for _, listStatus := range []int{0, http.StatusNotFound} {
			t.Run(fmt.Sprintf("%s/list%d", binding, listStatus), func(t *testing.T) {
				ec, requests := identityRunContext(t, identityService{
					listStatus: listStatus, getStatus: http.StatusNotFound,
				})
				file, pin := "golden.jsonl", ""
				switch binding {
				case "declared":
					pin = "7"
				case "recorded":
					ec.state = map[string]string{versionKey("dataset", "golden"): "7"}
				default:
					file = ""
				}
				ds, _, err := ec.buildRunDataSource(t.Context(), &project.Eval{Name: "quality", Dataset: "golden"},
					writeCatalog(t, file, pin), 0)
				require.Error(t, err)
				assert.Nil(t, ds)
				recorded := recordedIdentityRequests(requests)
				if binding != "no local file" {
					require.Len(t, recorded, 1, "a bound version must not be replaced by a registry/local fallback")
					assert.Equal(t, "/datasets/golden/versions/7", recorded[0].path)
					assert.True(t, dataset_api.IsNotFound(err), "the real version lookup failure remains typed")
				}
				for _, request := range recorded {
					assert.Equal(t, http.MethodGet, request.method)
				}
			})
		}
	}
}

func TestRunDatasetOverrideKeepsExistingRestrictions(t *testing.T) {
	_, err := withRunDatasetOverride(evalRef{ID: "eval_1"}, "golden")
	require.Error(t, err)
	_, err = withRunDatasetOverride(evalRef{Eval: &project.Eval{Name: "quality"}, Config: &project.EvalConfig{}}, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}
