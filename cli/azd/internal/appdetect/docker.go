package appdetect

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func detectDocker(path string, entries []fs.DirEntry) (*Docker, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "dockerfile" {
			dockerFilePath := filepath.Join(path, entry.Name())
			file, err := os.Open(dockerFilePath)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)

			var exposedPorts []int
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "EXPOSE") {
					parsedPorts, _ := parsePorts(line[len("EXPOSE"):])
					exposedPorts = append(exposedPorts, parsedPorts...)
				}
			}

			return &Docker{
				Path:         dockerFilePath,
				ExposedPorts: exposedPorts,
			}, nil
		}
	}

	return nil, nil
}

func parsePorts(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	var ports []int
	portSpecs := strings.Split(s, " ")
	for _, portSpec := range portSpecs {
		var numberString string
		if strings.Contains(portSpec, "/") {
			numberString = strings.Split(portSpec, "/")[0]
		} else {
			numberString = portSpec
		}
		port, err := strconv.Atoi(numberString)
		if err != nil {
			return nil, err
		}
		ports = append(ports, port)
	}
	return ports, nil
}
