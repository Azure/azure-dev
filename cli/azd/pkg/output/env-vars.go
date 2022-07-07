// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"fmt"
	"io"

	"github.com/joho/godotenv"
)

type EnvVarsFormatter struct {
}

func (f *EnvVarsFormatter) Kind() Format {
	return EnvVarsFormat
}

func (f *EnvVarsFormatter) Format(obj interface{}, writer io.Writer, _ interface{}) error {
	values, ok := obj.(map[string]string)
	if !ok {
		return fmt.Errorf("EnvVarsFormatter can only format objects of type map[string]string")
	}

	content, err := godotenv.Marshal(values)
	if err != nil {
		return fmt.Errorf("could not format values: %w", err)
	}

	_, err = writer.Write([]byte(content))
	if err != nil {
		return err
	}

	_, err = writer.Write([]byte("\n"))
	if err != nil {
		return err
	}

	return nil
}

var _ Formatter = (*EnvVarsFormatter)(nil)
