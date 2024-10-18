package appdetect

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func detectDocker(path string, entries []fs.DirEntry) (*Docker, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "dockerfile" {
			return &Docker{
				Path: filepath.Join(path, entry.Name()),
			}, nil
		}
	}

	return nil, nil
}

func detectPortInDockerfile(
	filePath string) []int {
	file, err := os.Open(filePath)
	if err != nil {
		return []int{}
	}
	defer file.Close()

	var result []int
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "EXPOSE") {
			var port int
			_, err := fmt.Sscanf(line, "EXPOSE %d", &port)
			if err == nil {
				result = append(result, port)
			}
		}
	}
	return result
}

type Docker struct {
	Path         string
	ExposedPorts []int
}
