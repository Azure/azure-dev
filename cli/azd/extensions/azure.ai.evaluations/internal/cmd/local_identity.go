// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"encoding/json"
	"slices"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
)

func localRequestKey(name string) string {
	return project.FingerprintKey("eval", name) + "_LOCAL_REQUEST_V1"
}

// localRequestFingerprint covers only immutable service configuration, not the
// local path, row contents, run cap, name or mutable description.
func localRequestFingerprint(req *eval_api.CreateOpenAIEvalRequest) (string, error) {
	criteria := slices.Clone(req.TestingCriteria)
	slices.SortFunc(criteria, func(a, b eval_api.TestingCriterion) int { return cmp.Compare(a.Name, b.Name) })
	body, err := json.Marshal(struct {
		Source   *eval_api.DataSourceConfig
		Criteria []eval_api.TestingCriterion
	}{req.DataSourceConfig, criteria})
	if err != nil {
		return "", err
	}
	return project.FingerprintBytes(body), nil
}

func localRequestMatchesRemote(req *eval_api.CreateOpenAIEvalRequest, remote *eval_api.OpenAIEval) (bool, error) {
	if remote == nil || len(remote.DataSourceConfig) == 0 {
		return false, nil
	}
	raw, err := json.Marshal(remote.DataSourceConfig)
	if err != nil {
		return false, err
	}
	var source eval_api.DataSourceConfig
	if err := json.Unmarshal(raw, &source); err != nil {
		return false, err
	}
	wanted, err := localRequestFingerprint(req)
	if err != nil {
		return false, err
	}
	criteria := slices.Clone(remote.TestingCriteria)
	for i := range criteria {
		stored := &criteria[i]
		if stored.EvaluatorVersion == "latest" && slices.ContainsFunc(req.TestingCriteria,
			func(desired eval_api.TestingCriterion) bool {
				return desired.EvaluatorVersion == "" && desired.Type == stored.Type &&
					desired.Name == stored.Name && desired.EvaluatorName == stored.EvaluatorName
			}) {
			// Only a service echo of this unpinned criterion is equivalent to omission.
			stored.EvaluatorVersion = ""
		}
	}
	actual, err := localRequestFingerprint(&eval_api.CreateOpenAIEvalRequest{
		DataSourceConfig: &source, TestingCriteria: criteria,
	})
	return wanted == actual, err
}
