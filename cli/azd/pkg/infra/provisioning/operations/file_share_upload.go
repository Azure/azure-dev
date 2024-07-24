// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package operations

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

type FileShareUpload struct {
	Description    string
	StorageAccount string
	FileShareName  string
	Path           string
}

var ErrBindMountOperationDisabled = fmt.Errorf(
	"%sYour project has bind mounts.\n  - %w\n%s\n",
	output.WithWarningFormat("*Note: "),
	ErrAzdOperationsNotEnabled,
	output.WithWarningFormat("Ignoring bind mounts."),
)

func FileShareUploads(model AzdOperationsModel) ([]FileShareUpload, error) {
	var fileShareUploadOperations []FileShareUpload
	for _, operation := range model.Operations {
		if operation.Type == fileShareUploadOperation {
			var fileShareUpload FileShareUpload
			bytes, err := json.Marshal(operation.Config)
			if err != nil {
				return nil, err
			}
			err = json.Unmarshal(bytes, &fileShareUpload)
			if err != nil {
				return nil, err
			}
			fileShareUpload.Description = operation.Description
			fileShareUploadOperations = append(fileShareUploadOperations, fileShareUpload)
		}
	}
	return fileShareUploadOperations, nil
}

func DoBindMount(
	ctx context.Context,
	fileShareUploadOperations []FileShareUpload,
	env *environment.Environment,
	console input.Console,
	fileShareService storage.FileShareService,
	cloudStorageEndpointSuffix string,
) error {
	if len(fileShareUploadOperations) > 0 {
		console.ShowSpinner(ctx, "uploading files to fileShare", input.StepFailed)
	}
	for _, op := range fileShareUploadOperations {
		if err := bindMountOperation(
			ctx,
			fileShareService,
			cloudStorageEndpointSuffix,
			env.GetSubscriptionId(),
			op.StorageAccount,
			op.FileShareName,
			op.Path); err != nil {
			return fmt.Errorf("error binding mount: %w", err)
		}
		console.MessageUxItem(ctx, &ux.DisplayedResource{
			Type:  fileShareUploadOperation,
			Name:  op.Description,
			State: ux.SucceededState,
		})
	}
	return nil
}

func bindMountOperation(
	ctx context.Context,
	fileShareService storage.FileShareService,
	cloud, subId, storageAccount, fileShareName, source string) error {

	shareUrl := fmt.Sprintf("https://%s.file.%s/%s", storageAccount, cloud, fileShareName)
	return fileShareService.UploadPath(ctx, subId, shareUrl, source)
}
