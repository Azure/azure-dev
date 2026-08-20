// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/protobuf/types/known/structpb"
)

func isServiceEnabled(
	ctx context.Context,
	src Source,
	envName string,
	serviceName string,
) (bool, error) {
	value, found, err := src.ServiceConfigValue(
		ctx,
		serviceName,
		"condition",
	)
	if err != nil {
		return false, err
	}
	if !found || value == nil {
		return true, nil
	}

	condition, err := conditionValueString(value)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(condition) == "" {
		return true, nil
	}

	expanded, err := expandServiceCondition(
		ctx,
		src,
		envName,
		condition,
	)
	if err != nil {
		return false, err
	}
	return isTruthyCondition(expanded), nil
}

func conditionValueString(value *structpb.Value) (string, error) {
	if value == nil {
		return "", nil
	}

	switch kind := value.Kind.(type) {
	case *structpb.Value_StringValue:
		return kind.StringValue, nil
	case *structpb.Value_BoolValue:
		return strconv.FormatBool(kind.BoolValue), nil
	case *structpb.Value_NumberValue:
		return strconv.FormatFloat(kind.NumberValue, 'g', -1, 64), nil
	case *structpb.Value_NullValue:
		return "", nil
	default:
		return "", fmt.Errorf(
			"condition must be a string, boolean, or number",
		)
	}
}

func expandServiceCondition(
	ctx context.Context,
	src Source,
	envName string,
	condition string,
) (string, error) {
	if envName == "" {
		return foundry.ExpandEnv(condition, os.Getenv)
	}

	values := map[string]string{}
	var lookupErr error
	expanded, err := foundry.ExpandEnv(condition, func(name string) string {
		if value, ok := values[name]; ok {
			return value
		}
		value, err := src.EnvValue(ctx, envName, name)
		if err != nil {
			lookupErr = fmt.Errorf(
				"read condition environment variable %q: %w",
				name,
				err,
			)
			return ""
		}
		values[name] = value
		return value
	})
	if err != nil {
		return "", err
	}
	if lookupErr != nil {
		return "", lookupErr
	}
	return expanded, nil
}

func isTruthyCondition(value string) bool {
	switch value {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes":
		return true
	default:
		return false
	}
}
