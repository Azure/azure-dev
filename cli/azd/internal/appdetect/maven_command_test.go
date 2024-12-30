package appdetect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetMvnwCommandInProject(t *testing.T) {
	cases := []struct {
		pomPath     string
		expected    string
		description string
	}{
		{"project1/pom.xml", "project1/mvnw", "Wrapper in same directory"},
		{"project2/sub-dir/pom.xml", "project2/mvnw", "Wrapper in parent directory"},
		{"project3/sub-dir/sub-sub-dir/pom.xml", "project3/mvnw", "Wrapper in grandparent directory"},
		{"project4/pom.xml", "", "No wrapper found"},
	}

	for _, c := range cases {
		t.Run(c.description, func(t *testing.T) {
			tempDir, err := os.MkdirTemp("", "testdata")
			if err != nil {
				t.Fatal(err)
			}
			defer func(path string) {
				err := os.RemoveAll(path)
				if err != nil {
					t.Errorf("failed to remove temp directory")
				}
			}(tempDir)

			pomPath := filepath.Join(tempDir, c.pomPath)
			err = os.MkdirAll(filepath.Dir(pomPath), os.ModePerm)
			if err != nil {
				t.Errorf("failed to mkdir")
			}
			err = os.WriteFile(pomPath, []byte("<project></project>"), 0600)
			if err != nil {
				t.Errorf("failed to write file")
			}
			if c.expected != "" {
				expectedPath := filepath.Join(tempDir, c.expected)
				err = os.WriteFile(expectedPath, []byte("#!/bin/sh"), 0600)
				if err != nil {
					t.Errorf("failed to write file")
				}
			}

			result, _ := getMvnwCommand(pomPath)
			expectedResult := ""
			if c.expected != "" {
				expectedResult = filepath.Join(tempDir, c.expected)
			}
			if result != expectedResult {
				t.Errorf("getMvnw(%q) == %q, expected %q", pomPath, result, expectedResult)
			}
		})
	}
}

func TestGetDownloadedMvnCommand(t *testing.T) {
	maven, err := getDownloadedMvnCommand()
	if err != nil {
		t.Errorf("getDownloadedMvnCommand failed, %v", err)
	}
	if maven == "" {
		t.Errorf("getDownloadedMvnCommand failed")
	}
}

func TestGetMvnCommand(t *testing.T) {
	maven, err := getMvnCommand()
	if err != nil {
		t.Errorf("getMvnCommand failed, %v", err)
	}
	if maven == "" {
		t.Errorf("getMvnCommand failed")
	}
}
