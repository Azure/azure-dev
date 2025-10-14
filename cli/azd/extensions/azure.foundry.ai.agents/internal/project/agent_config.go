// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)



func createBuildContext(dockerfilePath string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer tw.Close()

	// Get the directory containing the Dockerfile
	dockerfileDir := filepath.Dir(dockerfilePath)

	// Walk through the directory and add files to tar
	err := filepath.Walk(dockerfileDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path from dockerfile directory
		relPath, err := filepath.Rel(dockerfileDir, path)
		if err != nil {
			return err
		}

		// Convert Windows path separators to Unix style for tar
		relPath = filepath.ToSlash(relPath)

		// Create tar header
		header := &tar.Header{
			Name:    relPath,
			Size:    info.Size(),
			Mode:    int64(info.Mode()),
			ModTime: info.ModTime(),
		}

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Read and write file content
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create tar archive: %w", err)
	}

	return buf.Bytes(), nil
}

// GenerateImageNamesFromAgent generates image names using the agent ID
func GenerateImageNamesFromAgent(agentID string, customVersion string) []string {
	// Use agent ID as the base image name
	imageName := strings.ToLower(strings.ReplaceAll(agentID, "_", "-"))

	// Use custom version if provided, otherwise use timestamp
	var version string
	if customVersion != "" {
		version = customVersion
	} else {
		version = time.Now().Format("20060102-150405")
	}

	// Return array with only the version tag (no latest tag)
	return []string{
		fmt.Sprintf("%s:%s", imageName, version),
	}
}

// ACRTaskRun represents the request body for starting an ACR task run
type ACRTaskRun struct {
	Type           string   `json:"type"`
	IsArchive      bool     `json:"isArchiveEnabled"`
	SourceLocation string   `json:"sourceLocation"`
	DockerFilePath string   `json:"dockerFilePath"`
	ImageNames     []string `json:"imageNames"`
	IsPushEnabled  bool     `json:"isPushEnabled"`
	Platform       Platform `json:"platform"`
}

type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

// ACRRunResponse represents the response from starting an ACR task run
type ACRRunResponse struct {
	RunID  string `json:"runId"`
	Status string `json:"status"`
}

// ACRRunStatus represents the status response for a run
type ACRRunStatus struct {
	RunID     string    `json:"runId"`
	Status    string    `json:"status"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime,omitempty"`
}
