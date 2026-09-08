// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"io"
	"path"
	"sort"
	"strings"

	"azureaieval/internal/messages"
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
	// OpenDatasetContent already resolves the two shapes; the same rule applies
	// here, because the extension on the last segment is a guess either way.
	// Settled before the metadata read, which only the container path needs.
	if looksLikeBlobURI(sasURI) {
		if body, err := c.openDataset(ctx, sasURI); err == nil {
			_ = body.Close()
			return &DatasetContent{
				Container:  sasURI,
				Files:      []string{""},
				SingleFile: true,
				blobURI:    true,
			}, nil
		}
		// Not a blob after all: fall through and list it as a container.
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

	// Not from isSingleFile: a generated container reports it true as well, and
	// believing it wrote whichever entry sorted first -- `_meta.json` beside the
	// rows -- as though it were the dataset. What the credential names is the
	// only thing that actually distinguishes the two shapes.
	return &DatasetContent{
		Container: sasURI,
		Files:     files,
	}, nil
}

// Open starts reading one of the entries.
func (c *DatasetClient) Open(
	ctx context.Context,
	content *DatasetContent,
	file string,
) (io.ReadCloser, error) {
	if content.blobURI {
		return c.openDataset(ctx, content.Container)
	}
	return c.openBlob(ctx, content.Container, file)
}

// Extension is the suffix a single-file dataset should be written with, taken
// from what the service stored rather than assumed.
func (d *DatasetContent) Extension() string {
	if len(d.Files) == 0 {
		return ""
	}
	return path.Ext(d.Files[0])
}
