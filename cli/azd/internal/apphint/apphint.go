// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package apphint

import (
	"fmt"
	"strings"
)

type ApplicationType string

const (
	// A front-end SPA / static web app.
	WebApp ApplicationType = "web"
	// API only.
	ApiApp ApplicationType = "api"
	// Fullstack solution. Front-end SPA with back-end API.
	ApiWeb ApplicationType = "api-web"
)

type Language string

const (
	DotNet Language = "dotnet"
	Java   Language = "java"
	NodeJs Language = "nodejs"
	Python Language = "python"
)

type Framework string

const (
	// Or just frontend?
	React   Framework = "react"
	Angular Framework = "angular"
	VueJs   Framework = "vuejs"
	JQuery  Framework = "jquery"
)

func displayFramework(f Framework) string {
	switch f {
	case React:
		return "React"
	case Angular:
		return "Angular"
	case VueJs:
		return "Vue.js"
	case JQuery:
		return "JQuery"
	}

	return ""
}

type Project struct {
	Language         string
	WebFrameworks    []Framework
	FrameworkVersion string
	Path             string
	InferRule        string
	Docker           *Docker
}

type Docker struct {
	Path string
}

type Application struct {
	Type        ApplicationType
	Projects    []Project
	DisplayName string
}

func (a *Application) String() string {
	return a.DisplayName
}

type LanguageDetector interface {
	DetectProjects(root string) ([]Project, error)
}

func Analyze(root string) (*Application, error) {
	detectors := []LanguageDetector{
		&PythonDetector{},
		&NodeJsDetector{},
	}

	app := Application{}

	for _, detector := range detectors {
		projects, err := detector.DetectProjects(root)
		if err != nil {
			return &Application{}, err
		}

		app.Projects = append(app.Projects, projects...)
	}

	app = detectType(app)

	// ToDo: React inside dotnet API
	// Need to ensure mutual exclusion

	// Container app needs to go here

	return &app, nil
}

func detectType(app Application) Application {
	hasOneWebApp := false
	var webLanguage string
	apiLanguages := []string{}
	for _, proj := range app.Projects {
		if proj.WebFrameworks != nil && len(proj.WebFrameworks) > 0 {
			hasOneWebApp = true
			webLanguage = displayFramework(proj.WebFrameworks[0])
		} else {
			apiLanguages = append(apiLanguages, proj.Language)
		}
	}

	if len(app.Projects) == 1 && hasOneWebApp {
		app.Type = WebApp
		app.DisplayName = fmt.Sprintf("Web Application (%s)", webLanguage)
	} else if hasOneWebApp {
		app.Type = ApiWeb
		app.DisplayName = fmt.Sprintf("Web Application (%s) with API (%s)", webLanguage, strings.Join(apiLanguages, ", "))
	} else {
		app.Type = ApiApp
		app.DisplayName = fmt.Sprintf("Web API (%s)", strings.Join(apiLanguages, ", "))
	}

	return app
}
