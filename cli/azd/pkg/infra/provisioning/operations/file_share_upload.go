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

// FileShareUpload defines the configuration for a file share upload operation.
// When the operation is executed, the files in the specified path are uploaded to the specified file share.
type FileShareUpload struct {
	Description    string
	StorageAccount string
	FileShareName  string
	Path           string
}

// ErrBindMountOperationDisabled is returned when bind mount operations are disabled.
var ErrBindMountOperationDisabled = fmt.Errorf(
	"%sYour project has bind mounts.\n  - %w\n%s\n",
	output.WithWarningFormat("*Note: "),
	ErrAzdOperationsNotEnabled,
	output.WithWarningFormat("Ignoring bind mounts."),
)

// FileShareUploads returns the file share upload operations (if any) from the azd operations model.
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

// DoFileShareUpload performs the bind mount operations.
// It uploads the files in the specified path to the specified file share.
func DoFileShareUpload(
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
		shareUrl := fmt.Sprintf("https://%s.file.%s/%s", op.StorageAccount, cloudStorageEndpointSuffix, op.FileShareName)
		if err := fileShareService.UploadPath(ctx, env.GetSubscriptionId(), shareUrl, op.Path); err != nil {
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
