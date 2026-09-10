// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"fmt"
	"os"

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

	var lookupErr error
	enabled, err := evaluateCondition(
		conditionRawValue(value),
		serviceConditionLookup(ctx, src, envName, &lookupErr),
	)
	if err != nil {
		return false, err
	}
	if lookupErr != nil {
		return false, lookupErr
	}
	return enabled, nil
}

func conditionRawValue(value *structpb.Value) any {
	if value == nil {
		return nil
	}
	switch value.Kind.(type) {
	case *structpb.Value_NullValue:
		return nil
	default:
		return value.AsInterface()
	}
}

func serviceConditionLookup(
	ctx context.Context,
	src Source,
	envName string,
	lookupErr *error,
) func(string) string {
	if envName == "" {
		return os.Getenv
	}

	values := map[string]string{}
	return func(name string) string {
		if value, ok := values[name]; ok {
			return value
		}
		value, err := src.EnvValue(ctx, envName, name)
		if err != nil {
			*lookupErr = fmt.Errorf(
				"read condition environment variable %q: %w",
				name,
				err,
			)
			return ""
		}
		values[name] = value
		return value
	}
}
