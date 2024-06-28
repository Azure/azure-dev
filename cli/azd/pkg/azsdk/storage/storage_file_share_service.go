package storage

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/service"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azfile/share"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

type FileShareService interface {
	// Upload files from source path to a file share
	UploadPath(ctx context.Context, subId, shareUrl, source string) error
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

func (f *fileShareClient) UploadPath(ctx context.Context, subId, shareUrl, source string) error {
	credential, err := f.accountCreds.CredentialForSubscription(ctx, subId)
	if err != nil {
		return err
	}

	return filepath.WalkDir(source, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			destination := strings.TrimPrefix(path, source+string(filepath.Separator))
			if err := f.uploadFile(ctx, subId, shareUrl, path, destination, credential); err != nil {
				return fmt.Errorf("error uploading file to file share: %w", err)
			}
		}
		return nil
	})

}

// uploadFile implements FileShareService.
func (f *fileShareClient) uploadFile(
	ctx context.Context, subId, fileShareUrl, source, dest string, credential azcore.TokenCredential) error {

	client, err := share.NewClient(fileShareUrl, credential, &share.ClientOptions{
		ClientOptions:     f.options.ClientOptions,
		FileRequestIntent: to.Ptr(service.ShareTokenIntentBackup),
	})
	if err != nil {
		return err
	}

	dirClient := client.NewRootDirectoryClient()
	dirPaths := strings.Split(dest, string(os.PathSeparator))
	incrementPath := ""
	for _, dirPath := range dirPaths[:len(dirPaths)-1] {
		incrementPath = filepath.Join(incrementPath, dirPath)
		dirClient = client.NewDirectoryClient(incrementPath)
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
