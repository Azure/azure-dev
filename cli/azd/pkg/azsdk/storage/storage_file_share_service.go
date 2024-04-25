package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/service"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

type FileShareService interface {
	// Upload files from source path to a file share
	UploadFiles(ctx context.Context, subId, fileShareUrl, source, dest string) error
}

func NewFileShareService(
	accountCreds account.SubscriptionCredentialProvider,
	options *arm.ClientOptions,
) FileShareService {
	return &fileShareClient{
		accountCreds: accountCreds,
		options:      options,
	}
}

type fileShareClient struct {
	accountCreds account.SubscriptionCredentialProvider
	options      *arm.ClientOptions
}

// UploadFiles implements FileShareService.
func (f *fileShareClient) UploadFiles(ctx context.Context, subId, fileShareUrl, source, dest string) error {
	credential, err := f.accountCreds.CredentialForSubscription(ctx, subId)
	if err != nil {
		return err
	}

	client, err := share.NewClient(fileShareUrl, credential, &share.ClientOptions{
		ClientOptions:     f.options.ClientOptions,
		FileRequestIntent: to.Ptr(service.ShareTokenIntentBackup),
	})
	if err != nil {
		return err
	}

	dirClient := client.NewRootDirectoryClient()
	dirPath := filepath.Dir(dest)
	if dirPath != "." {
		dirClient = client.NewDirectoryClient(dirPath)
		if _, err := dirClient.Create(ctx, nil); err != nil {
			if !strings.Contains(err.Error(), "ResourceAlreadyExists") {
				return err
			}
		}
	}

	file, err := os.OpenFile(source, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()

	fInfo, err := file.Stat()
	if err != nil {
		return err
	}
	fSize := fInfo.Size()

	fileName := filepath.Base(dest)
	fClient := dirClient.NewFileClient(fileName)
	if _, err := fClient.Create(ctx, fSize, nil); err != nil {
		return err
	}

	return fClient.UploadFile(ctx, file, nil)
}
