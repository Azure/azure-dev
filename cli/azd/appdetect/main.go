package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
)

var repoDirectory string
var directory string
var projectDirectory string

func init() {
	flag.StringVar(&repoDirectory, "repo", "", "Path to repository to detect")
	flag.StringVar(&directory, "dir", "", "Directory containing projects to detect")
	flag.StringVar(&projectDirectory, "proj", "", "Specific project directory to detect")

}

func main() {
	flag.Parse()

	if repoDirectory == "" && directory == "" && projectDirectory == "" {
		fmt.Println("must set one of 'repo', 'proj', or 'dir'")
		os.Exit(1)
	}

	var projects []appdetect.Project
	var err error

	if repoDirectory != "" {
		projects, err = appdetect.Detect(repoDirectory)
	}

	if directory != "" {
		projects, err = appdetect.DetectUnder(directory)
	}

	if projectDirectory != "" {
		var project *appdetect.Project
		project, err = appdetect.DetectDirectory(projectDirectory)

		if err == nil && project != nil {
			projects = append(projects, *project)
		}
	}

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Println("Projects detected:")
	fmt.Printf("%v\n", projects)
}
