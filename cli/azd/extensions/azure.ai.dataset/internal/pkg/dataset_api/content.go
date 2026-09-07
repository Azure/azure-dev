// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"

	"azureaidataset/internal/messages"
	"azureaidataset/internal/urlsafe"
)

// DatasetContent is what one dataset version holds, ready to be written out.
//
// A version is a container, not a file. The single-blob path this package
// already had picks one blob and calls it the dataset, which is right for
// reading rows and wrong for downloading: a folder dataset came back as
// whichever file sorted first, silently short by everything else.
type DatasetContent struct {
	// Container is the SAS-bearing container URI every entry is read from.
	Container string
	// Files are the entries' paths relative to the dataset root, sorted so two
	// downloads of one version lay out the same way.
	Files []string
	// SingleFile is what the service says about the shape, not what the file
	// count happens to be: a folder dataset holding one file is still a folder,
	// and writing it as a bare file loses the name it had inside.
	SingleFile bool
}

// ListDatasetContent enumerates the files a dataset version holds.
func (c *DatasetClient) ListDatasetContent(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (*DatasetContent, error) {
	meta, err := c.GetDataset(ctx, name, version, apiVersion)
	if err != nil {
		return nil, err
	}

	cred, err := c.GetDatasetCredential(ctx, name, version, apiVersion)
	if err != nil {
		return nil, messages.ReadingDownloadCredentials(name, err)
	}
	sasURI := cred.ResolvedDownloadURI()
	if sasURI == "" {
		return nil, messages.NoDownloadURI(name)
	}

	names, err := c.ListContainerBlobs(ctx, sasURI)
	if err != nil {
		return nil, messages.ListingDatasetContent(name, err)
	}

	files := make([]string, 0, len(names))
	for _, n := range names {
		// A directory marker is not a file to write, and an empty name is not
		// anything at all.
		if n == "" || strings.HasSuffix(n, "/") {
			continue
		}
		files = append(files, n)
	}
	if len(files) == 0 {
		return nil, messages.DatasetHasNoFile(name)
	}
	sort.Strings(files)

	return &DatasetContent{
		Container:  sasURI,
		Files:      files,
		SingleFile: meta.IsSingleFile,
	}, nil
}

// Open starts reading one of the entries.
//
// Streamed rather than read into memory: a dataset is as large as somebody made
// it, and holding a whole file to copy it to disk fails on exactly the datasets
// worth downloading.
func (c *DatasetClient) Open(
	ctx context.Context,
	content *DatasetContent,
	file string,
) (io.ReadCloser, error) {
	u, err := url.Parse(content.Container)
	if err != nil {
		return nil, messages.InvalidContainerURI(urlsafe.Error(err))
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + file

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, messages.CreatingBlobDownloadRequest(urlsafe.Error(err))
	}
	resp, err := blobHTTPClient.Do(req)
	if err != nil {
		return nil, messages.DownloadingBlob(urlsafe.Error(err))
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, messages.BlobDownloadStatusFor(resp.StatusCode, file)
	}
	return resp.Body, nil
}

// Extension is the suffix a single-file dataset should be written with, taken
// from what the service stored rather than assumed.
func (d *DatasetContent) Extension() string {
	if len(d.Files) == 0 {
		return ""
	}
	return path.Ext(d.Files[0])
}
