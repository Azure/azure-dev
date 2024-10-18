package appdetect

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func detectDocker(path string, entries []fs.DirEntry) (*Docker, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "dockerfile" {
			dockerFilePath := filepath.Join(path, entry.Name())
			exposedPorts, _ := getExposedPorts(dockerFilePath)
			return &Docker{
				Path:         dockerFilePath,
				ExposedPorts: exposedPorts,
			}, nil
		}
	}

	return nil, nil
}

func getExposedPorts(dockerfilePath string) (map[int]string, error) {
	file, err := os.Open(dockerfilePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	ports := make(map[int]string)
	scanner := bufio.NewScanner(file)
	exposeRegex := regexp.MustCompile(`^EXPOSE\s+(.+)$`)

	for scanner.Scan() {
		line := scanner.Text()
		matches := exposeRegex.FindStringSubmatch(line)
		if len(matches) == 2 {
			portSpecs := strings.Fields(matches[1])
			for _, portSpec := range portSpecs {
				parts := strings.Split(portSpec, "/")
				port, err := strconv.Atoi(parts[0])
				if err != nil {
					return nil, err
				}
				protocol := "tcp"
				if len(parts) > 1 {
					protocol = parts[1]
				}
				ports[port] = protocol
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return ports, nil
}

type Docker struct {
	Path         string
	ExposedPorts map[int]string
}
