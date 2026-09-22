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
// Every file is included: selecting one blob from a folder would silently
// download only part of the dataset.
type DatasetContent struct {
	// Container is the SAS-bearing container URI every entry is read from.
	Container string
	// Files are the entries' paths relative to the dataset root, sorted so two
	// downloads of one version lay out the same way.
	Files []string
	// SingleFile identifies a direct blob, or a fully enumerated single entry
	// whose dataset metadata confirms it is a file rather than a folder.
	SingleFile bool
	// blobURI records that Container already names the blob, so reading it must
	// not append an entry name to the path.
	blobURI bool
}

// ListDatasetContent enumerates the files a dataset version holds.
func (c *DatasetClient) ListDatasetContent(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (*DatasetContent, error) {
	cred, err := c.GetDatasetCredential(ctx, name, version, apiVersion)
	if err != nil {
		return nil, messages.ReadingDownloadCredentials(name, err)
	}
	sasURI := cred.ResolvedDownloadURI()
	if sasURI == "" {
		return nil, messages.NoDownloadURI(name)
	}

	// An uploaded dataset's URI names the blob itself, and listing one answers
	// 409 -- so `dataset download` failed for every dataset this CLI published.
	probedAsBlob := false
	if looksLikeBlobURI(sasURI) {
		probedAsBlob = true
		if body, err := openBlobURL(ctx, sasURI, name); err == nil {
			_ = body.Close()
			// Its own name, not an empty one: the entry is what Extension reads,
			// and without it a single-file download lands as `name-version` with
			// the suffix the service stored dropped.
			return &DatasetContent{
				Container:  sasURI,
				Files:      []string{blobNameFromURI(sasURI)},
				SingleFile: true,
				blobURI:    true,
			}, nil
		}
		// Not a blob after all: fall through and list it as a container.
	}

	names, err := c.ListContainerBlobs(ctx, sasURI)
	if err != nil {
		// A blob name does not have to carry an extension, so the guess above can
		// send a single blob down the container path. The listing failing is what
		// says so -- ask storage before reporting a dataset that reads fine as
		// one that cannot be listed.
		if !probedAsBlob {
			if body, probeErr := openBlobURL(ctx, sasURI, name); probeErr == nil {
				_ = body.Close()
				return &DatasetContent{
					Container:  sasURI,
					Files:      []string{blobNameFromURI(sasURI)},
					SingleFile: true,
					blobURI:    true,
				}, nil
			}
		}
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

	// A container SAS is an access scope, not the dataset's shape. Only consult
	// metadata after enumerating every entry: generated multi-file containers
	// can also report isSingleFile, and must never be shortened to one file.
	singleFile := false
	if len(files) == 1 {
		dataset, err := c.GetDataset(ctx, name, version, apiVersion)
		if err != nil {
			return nil, messages.ReadingDatasetVersion(name, version, err)
		}
		singleFile = dataset.IsSingleFile
	}

	return &DatasetContent{
		Container:  sasURI,
		Files:      files,
		SingleFile: singleFile,
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
	// Already the blob when the credential named one; appending an entry would
	// address a path that does not exist.
	if !content.blobURI {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + file
	}
	return openBlobURL(ctx, u.String(), file)
}

// openBlobURL reads one blob by its SAS URL.
//
// A plain client, not the SDK pipeline: the SAS in the URL is the credential,
// and the pipeline's bearer token and correlation headers have no business
// reaching storage.
func openBlobURL(ctx context.Context, blobURL, name string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
	if err != nil {
		return nil, messages.CreatingBlobDownloadRequest(urlsafe.Error(err))
	}
	resp, err := blobHTTPClient.Do(req)
	if err != nil {
		return nil, messages.DownloadingBlob(urlsafe.Error(err))
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, messages.BlobDownloadStatusFor(resp.StatusCode, name)
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

// blobNameFromURI is the file name a blob SAS points at, without its query.
func blobNameFromURI(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return path.Base(u.Path)
}
