// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// EvaluateCondition reports whether a service condition enables the
// service. A missing, null, or empty condition is enabled. String
// values expand with ExpandEnv so ${VAR} matches other Foundry
// fields. True values match
// pkg/project.ServiceConfig.IsEnabled:
// 1, true, TRUE, True, yes, YES, Yes.
func EvaluateCondition(
	value any,
	getenv func(string) string,
) (bool, error) {
	if value == nil {
		return true, nil
	}

	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		return evaluateConditionString(v, getenv)
	case json.Number:
		return evaluateConditionString(string(v), getenv)
	case int:
		return isTruthyCondition(strconv.Itoa(v)), nil
	case int8:
		return isTruthyCondition(strconv.Itoa(int(v))), nil
	case int16:
		return isTruthyCondition(strconv.Itoa(int(v))), nil
	case int32:
		return isTruthyCondition(strconv.Itoa(int(v))), nil
	case int64:
		return isTruthyCondition(strconv.FormatInt(v, 10)), nil
	case uint:
		return isTruthyCondition(strconv.FormatUint(uint64(v), 10)), nil
	case uint8:
		return isTruthyCondition(strconv.FormatUint(uint64(v), 10)), nil
	case uint16:
		return isTruthyCondition(strconv.FormatUint(uint64(v), 10)), nil
	case uint32:
		return isTruthyCondition(strconv.FormatUint(uint64(v), 10)), nil
	case uint64:
		return isTruthyCondition(strconv.FormatUint(v, 10)), nil
	case float32:
		return isTruthyCondition(
			strconv.FormatFloat(float64(v), 'g', -1, 32),
		), nil
	case float64:
		return isTruthyCondition(
			strconv.FormatFloat(v, 'g', -1, 64),
		), nil
	default:
		return false, fmt.Errorf(
			"condition must be a string, boolean, or number",
		)
	}
}

func evaluateConditionString(
	value string,
	getenv func(string) string,
) (bool, error) {
	if strings.TrimSpace(value) == "" {
		return true, nil
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	expanded, err := ExpandEnv(value, getenv)
	if err != nil {
		return false, fmt.Errorf(
			"malformed condition template: %w",
			err,
		)
	}
	return isTruthyCondition(expanded), nil
}

func isTruthyCondition(value string) bool {
	switch value {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes":
		return true
	default:
		return false
	}
}
