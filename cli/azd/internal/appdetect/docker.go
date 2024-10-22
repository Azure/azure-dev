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

			var ports []Port
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "EXPOSE") {
					parsedPorts, err := parsePorts(line[len("EXPOSE"):])
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
	}

	return nil, nil
}

func parsePorts(s string) ([]Port, error) {
	var ports []Port
	portSpecs := strings.Fields(s)
	for _, portSpec := range portSpecs {
		if strings.Contains(portSpec, "/") {
			parts := strings.Split(portSpec, "/")
			portNumber, err := strconv.Atoi(parts[0])
			if err != nil {
				return nil, fmt.Errorf("parsing port number: %w", err)
			}
			protocol := parts[1]
			ports = append(ports, Port{portNumber, protocol})
		} else {
			portNumber, err := strconv.Atoi(portSpec)
			if err != nil {
				return nil, fmt.Errorf("parsing port number: %w", err)
			}
			ports = append(ports, Port{portNumber, "tcp"})
		}
	}
	return ports, nil
}
