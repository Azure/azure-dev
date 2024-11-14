package appdetect

import (
	"bufio"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func detectDockerInDirectory(path string, entries []fs.DirEntry) (*Docker, error) {
	for _, entry := range entries {
		if strings.ToLower(entry.Name()) == "dockerfile" {
			dockerFilePath := filepath.Join(path, entry.Name())
			return AnalyzeDocker(dockerFilePath)
		}
	}

	return nil, nil
}

// AnalyzeDocker analyzes the Dockerfile and returns the Docker result.
func AnalyzeDocker(dockerFilePath string) (*Docker, error) {
	file, err := os.Open(dockerFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading Dockerfile at %s: %w", dockerFilePath, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)

	var ports []Port
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "EXPOSE") {
			parsedPorts, err := parsePortsInLine(line[len("EXPOSE"):])
			if err != nil {
				log.Printf("parsing Dockerfile at %s: %v", dockerFilePath, err)
			}
			ports = append(ports, parsedPorts...)
		}
	}
	return &Docker{
		Path:  dockerFilePath,
		Ports: ports,
	}, nil
}

func parsePortsInLine(s string) ([]Port, error) {
	var ports []Port
	portSpecs := strings.Fields(s)
	for _, portSpec := range portSpecs {
		var portString string
		var protocol string
		if strings.Contains(portSpec, "/") {
			parts := strings.Split(portSpec, "/")
			portString = parts[0]
			protocol = parts[1]
		} else {
			portString = portSpec
			protocol = "tcp"
		}
		portNumber, err := strconv.Atoi(portString)
		if err != nil {
			return nil, fmt.Errorf("parsing port number: %w", err)
		}
		ports = append(ports, Port{portNumber, protocol})
	}
	return ports, nil
}
