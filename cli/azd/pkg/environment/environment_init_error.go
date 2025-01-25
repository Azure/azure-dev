// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import "fmt"

type EnvironmentInitError struct {
	Name string
}

func NewEnvironmentInitError(envName string) *EnvironmentInitError {
	return &EnvironmentInitError{Name: envName}
}

func (err *EnvironmentInitError) Error() string {
	return fmt.Sprintf("environment already initialized to %s", err.Name)
}
