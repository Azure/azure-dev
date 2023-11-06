package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
)

var directory string
var projectDirectory string

func init() {
	flag.StringVar(&directory, "dir", "", "Directory containing projects to detect")
	flag.StringVar(&projectDirectory, "proj", "", "Specific project directory to detect")
}

func main() {
	flag.Parse()
	ctx := context.Background()

	if directory == "" && projectDirectory == "" {
		fmt.Println("must set one of 'proj', or 'dir'")
		os.Exit(1)
	}

	var projects []appdetect.Project
	var err error

	if directory != "" {
		projects, err = appdetect.Detect(ctx, directory)
	}

	if projectDirectory != "" {
		var project *appdetect.Project
		project, err = appdetect.DetectDirectory(ctx, projectDirectory)

		if err == nil && project != nil {
			projects = append(projects, *project)
		}
	}

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Println("Projects detected:")
	content, err := json.Marshal(projects)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%v\n", string(content))
}
