// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package operations

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"gopkg.in/yaml.v3"
)

type azdOperation struct {
	Type        string
	Description string
	Config      any
}

type AzdOperationsModel struct {
	Operations []azdOperation
}

const (
	fileShareUploadOperation string = "FileShareUpload"
	sqlServerOperation       string = "SqlScript"
	azdOperationsFileName    string = "azd.operations.yaml"
)

var AzdOperationsFeatureKey = alpha.MustFeatureKey("azd.operations")

var ErrAzdOperationsNotEnabled = fmt.Errorf(fmt.Sprintf(
	"azd operations (alpha feature) is required but disabled. You can enable azd operations by running: %s",
	output.WithGrayFormat(alpha.GetEnableCommand(AzdOperationsFeatureKey))))

func AzdOperations(infraPath string, env environment.Environment) (AzdOperationsModel, error) {
	path := filepath.Join(infraPath, azdOperationsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// file not found is not an error, there's just nothing to do
			return AzdOperationsModel{}, nil
		}
		return AzdOperationsModel{}, err
	}

	// resolve environment variables
	expString := osutil.NewExpandableString(string(data))
	evaluated, err := expString.Envsubst(env.Getenv)
	if err != nil {
		return AzdOperationsModel{}, err
	}
	data = []byte(evaluated)

	// Unmarshal the file into azdOperationsModel
	var operations AzdOperationsModel
	err = yaml.Unmarshal(data, &operations)
	if err != nil {
		return AzdOperationsModel{}, err
	}

	return operations, nil
}
