// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	finetuneFilesPath                     = "/openai/v1/files"
	finetuneFileImportTimeout             = 5 * time.Minute
	finetuneFileImportInitialPollInterval = 2 * time.Second
	finetuneFileImportMaxPollInterval     = 10 * time.Second
)

type finetuneFileResource struct {
	Id            string `json:"id"`
	Status        string `json:"status,omitempty"`
	StatusDetails string `json:"status_details,omitempty"`
}

func (c *finetuneClient) uploadFile(ctx context.Context, filePath string) (*finetuneFileResource, error) {
	root, err := os.OpenRoot(filepath.Dir(filePath))
	if err != nil {
		return nil, fmt.Errorf("open local file directory: %w", err)
	}
	defer root.Close()

	file, err := root.Open(filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("open local file: %w", err)
	}

	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect local file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("local file must be a regular file")
	}

	bodyReader, bodyWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(bodyWriter)
	writeResult := make(chan error, 1)
	go func() {
		defer file.Close()

		if err := writeFinetuneFileUpload(multipartWriter, file, filepath.Base(filePath)); err != nil {
			_ = bodyWriter.CloseWithError(err)
			writeResult <- err
			return
		}
		writeResult <- bodyWriter.Close()
	}()

	var result finetuneFileResource
	requestErr := c.doWithReader(
		ctx,
		http.MethodPost,
		finetuneFilesPath,
		nil,
		bodyReader,
		multipartWriter.FormDataContentType(),
		&result,
	)
	if requestErr != nil {
		_ = bodyReader.CloseWithError(requestErr)
	} else {
		_ = bodyReader.Close()
	}
	writeErr := <-writeResult
	if requestErr != nil {
		return nil, fmt.Errorf("upload fine-tuning file: %w", requestErr)
	}
	if writeErr != nil {
		return nil, fmt.Errorf("write fine-tuning file upload: %w", writeErr)
	}
	return &result, nil
}

func (c *finetuneClient) getFile(ctx context.Context, fileID string) (*finetuneFileResource, error) {
	var result finetuneFileResource
	path := finetuneFilesPath + "/" + url.PathEscape(fileID)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *finetuneClient) waitForFileProcessed(
	ctx context.Context,
	file *finetuneFileResource,
	timeout time.Duration,
	initialPollInterval time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	pollInterval := initialPollInterval
	for {
		status := strings.ToLower(strings.TrimSpace(file.Status))
		switch status {
		case "processed":
			return nil
		case "error", "deleted", "failed", "cancelled", "canceled", "expired":
			return finetuneFileImportTerminalError(file)
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("fine-tuning file import %s did not reach processed within %s", file.Id, timeout)
		}
		if err := waitForFinetuneFilePoll(ctx, min(pollInterval, remaining)); err != nil {
			return err
		}

		updated, err := c.getFile(ctx, file.Id)
		if err != nil {
			return fmt.Errorf("get fine-tuning file import status: %w", err)
		}
		file = updated
		pollInterval = min(pollInterval*2, finetuneFileImportMaxPollInterval)
	}
}

func finetuneFileImportTerminalError(file *finetuneFileResource) error {
	if details := strings.TrimSpace(file.StatusDetails); details != "" {
		return fmt.Errorf("fine-tuning file import %s ended with status %q: %s", file.Id, file.Status, details)
	}
	return fmt.Errorf("fine-tuning file import %s ended with status %q", file.Id, file.Status)
}

func waitForFinetuneFilePoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func writeFinetuneFileUpload(writer *multipart.Writer, file *os.File, fileName string) error {
	if err := writer.WriteField("purpose", "fine-tune"); err != nil {
		return fmt.Errorf("write purpose field: %w", err)
	}

	filePart, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return fmt.Errorf("create file form part: %w", err)
	}
	if _, err := io.Copy(filePart, file); err != nil {
		return fmt.Errorf("write file content: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart form: %w", err)
	}
	return nil
}
