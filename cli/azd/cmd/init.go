package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/npm"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/fatih/color"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// --- Helper functions for TypeScript provider initialization ---
func initializeTypeScriptInfra(ctx context.Context, azdCtx *azdcontext.AzdContext) error {
	   infraDir := filepath.Join(azdCtx.ProjectDirectory(), "infra")
	   // Ensure infra directory exists
	   if err := os.MkdirAll(infraDir, 0755); err != nil {
			   return fmt.Errorf("failed to create infra directory: %w", err)
	   }
	   // Remove Bicep files if present
	   entries, err := os.ReadDir(infraDir)
	   if err == nil {
			   for _, entry := range entries {
					   if filepath.Ext(entry.Name()) == ".bicep" || filepath.Ext(entry.Name()) == ".json" {
							   os.Remove(filepath.Join(infraDir, entry.Name()))
					   }
			   }
	   }
	   // Only create deploy.ts if it does not exist (do not overwrite template-provided file)
	   deployTsPath := filepath.Join(infraDir, "deploy.ts")
	   if _, err := os.Stat(deployTsPath); err == nil {
			   // File exists, do not overwrite
	   } else if os.IsNotExist(err) {
			   deployTsContent := `import { DefaultAzureCredential } from "@azure/identity";
import { ResourceManagementClient } from "@azure/arm-resources";
import * as fs from "fs";

const subscriptionId = process.env.AZURE_SUBSCRIPTION_ID!;
const resourceGroupName = process.env.AZURE_ENV_NAME!;
const location = process.env.AZURE_LOCATION!;

async function main() {
	const credential = new DefaultAzureCredential();
	const client = new ResourceManagementClient(credential, subscriptionId);
	await client.resourceGroups.createOrUpdate(resourceGroupName, { location });
	// Add more resources here as needed
	const outputs = {
		resourceGroupName: { value: resourceGroupName },
		location: { value: location },
	};
	fs.writeFileSync("outputs.json", JSON.stringify(outputs, null, 2));
	console.log(JSON.stringify(outputs));
}

main().catch(err => { console.error(err); process.exit(1); });
`
			   if err := os.WriteFile(deployTsPath, []byte(deployTsContent), 0644); err != nil {
					   return fmt.Errorf("failed to write deploy.ts: %w", err)
			   }
	   } else if err != nil {
			   return fmt.Errorf("failed to stat deploy.ts: %w", err)
	   }

	// Create package.json
	packageJsonPath := filepath.Join(infraDir, "package.json")
	if _, err := os.Stat(packageJsonPath); os.IsNotExist(err) {
		packageJsonContent := `{
			"name": "infra",
			"version": "1.0.0",
			"main": "index.js",
			"scripts": {
				"build": "tsc -p tsconfig.build.json",
				"start": "node dist/deploy.js"
			},
			"author": "",
			"license": "ISC",
			"description": "",
			"dependencies": {
				"@azure/arm-resources": "^6.1.0",
				"@azure/identity": "^4.10.0",
				"typescript": "^5.8.3"
			},
			"devDependencies": {
				"@types/node": "^22.15.29"
			}
		}`
		if err := os.WriteFile(packageJsonPath, []byte(packageJsonContent), 0644); err != nil {
			return fmt.Errorf("failed to write package.json: %w", err)
		}
	}

	// Create tsconfig.json
	tsconfigJsonPath := filepath.Join(infraDir, "tsconfig.json")
	if _, err := os.Stat(tsconfigJsonPath); os.IsNotExist(err) {
		tsconfigJsonContent := `{
			"compilerOptions": {
				"target": "ES2020",
				"module": "CommonJS",
				"strict": true,
				"esModuleInterop": true,
				"allowSyntheticDefaultImports": true,
				"skipLibCheck": true,
				"forceConsistentCasingInFileNames": true,
				"outDir": "dist",
				"paths": {
					"http": ["./node_modules/@types/node"],
					"https": ["./node_modules/@types/node"]
				}
			},
			"include": ["deploy.ts"],
			"exclude": ["node_modules"]
		}`
		if err := os.WriteFile(tsconfigJsonPath, []byte(tsconfigJsonContent), 0644); err != nil {
			return fmt.Errorf("failed to write tsconfig.json: %w", err)
		}
	}

	// Create tsconfig.build.json
	tsconfigBuildJsonPath := filepath.Join(infraDir, "tsconfig.build.json")
	if _, err := os.Stat(tsconfigBuildJsonPath); os.IsNotExist(err) {
		tsconfigBuildJsonContent := `{
			"extends": "./tsconfig.json",
			"compilerOptions": {
				"noEmitOnError": false,
				"skipLibCheck": true
			},
			"include": ["deploy.ts"],
			"exclude": ["node_modules"]
		}`
		if err := os.WriteFile(tsconfigBuildJsonPath, []byte(tsconfigBuildJsonContent), 0644); err != nil {
			return fmt.Errorf("failed to write tsconfig.build.json: %w", err)
		}
	}

	// Use npm.Cli with CommandRunner to run npm install
	runnerOptions := &exec.RunnerOptions{}
	npmCli := npm.NewCli(exec.NewCommandRunner(runnerOptions))
	if err := npmCli.Install(ctx, infraDir); err != nil {
		return fmt.Errorf("failed to run npm install: %w", err)
	}

	// Create dist directory
	distDir := filepath.Join(infraDir, "dist")
	if err := os.MkdirAll(distDir, 0755); err != nil {
		return fmt.Errorf("failed to create dist directory: %w", err)
	}

	// Compile TypeScript to JavaScript with better error handling
	fmt.Printf("Compiling TypeScript in %s...\n", infraDir)
	if err := npmCli.RunScript(ctx, infraDir, "build"); err != nil {
		fmt.Printf("Error compiling TypeScript: %s\n", err)
		return fmt.Errorf("failed to compile TypeScript: %w", err)
	}
	
	// Check if dist/deploy.js was created successfully
	deployJsPath := filepath.Join(distDir, "deploy.js")
	if _, err := os.Stat(deployJsPath); os.IsNotExist(err) {
		// If compilation failed silently, create the dist directory and try to install dependencies again
		fmt.Printf("Warning: compiled deploy.js not found at %s after npm build. Attempting recovery...\n", deployJsPath)
		
		// Run tsc directly as a last resort
		tscCmd := osexec.CommandContext(ctx, "npx", "tsc", "-p", filepath.Join(infraDir, "tsconfig.build.json"))
		tscCmd.Dir = infraDir
		if tscOutput, tscErr := tscCmd.CombinedOutput(); tscErr != nil {
			fmt.Printf("TypeScript compilation failed: %s\n%s\n", tscErr, string(tscOutput))
		} else {
			fmt.Printf("TypeScript compilation completed via npx tsc\n")
		}
	} else {
		fmt.Printf("Successfully compiled TypeScript to %s\n", deployJsPath)
	}

	 	// Update azure.yaml to use TypeScript provider, or add an infra section if missing
	   azureYamlPath := filepath.Join(azdCtx.ProjectDirectory(), "azure.yaml")
	   if _, err := os.Stat(azureYamlPath); err == nil {
			   azureYaml, err := os.ReadFile(azureYamlPath)
			   if err != nil {
					   return fmt.Errorf("failed to read azure.yaml: %w", err)
			   }
			   newYaml := string(azureYaml)
			   if !strings.Contains(newYaml, "infra:") {
					   // Prepend an infra section if missing
					   newYaml = "infra:\n  provider: typescript\n  path: infra\n  module: main\n\n" + newYaml
					   fmt.Printf("Adding TypeScript infrastructure configuration to azure.yaml\n")
			   } else {
					   // Replace 'provider: bicep' with 'provider: typescript' if present
					   oldYaml := newYaml
					   newYaml = replaceInfraProvider(newYaml, "typescript")
					   if oldYaml != newYaml {
							   fmt.Printf("Updated provider in azure.yaml from bicep to typescript\n")
					   } else {
							   fmt.Printf("Infrastructure provider already configured in azure.yaml\n")
					   }
			   }
			   if err := os.WriteFile(azureYamlPath, []byte(newYaml), 0644); err != nil {
					   return fmt.Errorf("failed to update azure.yaml: %w", err)
			   }
	   } else if os.IsNotExist(err) {
			   // Create a minimal azure.yaml with TypeScript configuration
			   fmt.Printf("Creating new azure.yaml with TypeScript provider\n")
			   minimalYaml := "infra:\n  provider: typescript\n  path: infra\n  module: main\n"
			   if err := os.WriteFile(azureYamlPath, []byte(minimalYaml), 0644); err != nil {
					   return fmt.Errorf("failed to create azure.yaml: %w", err)
			   }
	   } else if err != nil {
			   return fmt.Errorf("failed to stat azure.yaml: %w", err)
	   }
	   return nil
}

// replaceInfraProvider replaces the provider in the infra section of azure.yaml
func replaceInfraProvider(yamlContent string, provider string) string {
	// Simple string replace for 'provider: bicep' or 'provider: "bicep"'
	out := yamlContent
	out = strings.ReplaceAll(out, "provider: bicep", "provider: "+provider)
	out = strings.ReplaceAll(out, "provider: \"bicep\"", "provider: \""+provider+"\"")
	return out
}


func newInitFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *initFlags {
	flags := &initFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new application.",
	}
}

type initFlags struct {
	   // TypeScript Provider flag
	tsp bool
	templatePath   string
	templateBranch string
	templateTags   []string
	subscription   string
	location       string
	global         *internal.GlobalCommandOptions
	fromCode       bool
	up             bool
	internal.EnvFlag
}

func (i *initFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(
			&i.tsp,
			"tsp",
			false,
			"Use the TypeScript infrastructure provider (deploy.ts) instead of Bicep.",
	)
	local.StringVarP(
		&i.templatePath,
		"template",
		"t",
		"",
		//nolint:lll
		"Initializes a new application from a template. You can use Full URI, <owner>/<repository>, or <repository> if it's part of the azure-samples organization.",
	)
	local.StringVarP(
		&i.templateBranch,
		"branch",
		"b",
		"",
		"The template branch to initialize from. Must be used with a template argument (--template or -t).")
	local.StringSliceVarP(
		&i.templateTags,
		"filter",
		"f",
		[]string{},
		"The tag(s) used to filter template results. Supports comma-separated values.",
	)
	local.StringVarP(
		&i.subscription,
		"subscription",
		"s",
		"",
		"Name or ID of an Azure subscription to use for the new environment",
	)
	local.BoolVarP(
		&i.fromCode,
		"from-code",
		"",
		false,
		"Initializes a new application from your existing code.",
	)
	local.BoolVarP(
		&i.up,
		"up",
		"",
		false,
		"Provision and deploy to Azure after initializing the project from a template.",
	)
	local.StringVarP(&i.location, "location", "l", "", "Azure location for the new environment")
	i.EnvFlag.Bind(local, global)

	i.global = global
}

type initAction struct {
	lazyAzdCtx        *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnvManager    *lazy.Lazy[environment.Manager]
	console           input.Console
	cmdRun            exec.CommandRunner
	gitCli            *git.Cli
	flags             *initFlags
	repoInitializer   *repository.Initializer
	templateManager   *templates.TemplateManager
	featuresManager   *alpha.FeatureManager
	extensionsManager *extensions.Manager
	azd               workflow.AzdCommandRunner
}

func newInitAction(
	lazyAzdCtx *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
	cmdRun exec.CommandRunner,
	console input.Console,
	gitCli *git.Cli,
	flags *initFlags,
	repoInitializer *repository.Initializer,
	templateManager *templates.TemplateManager,
	featuresManager *alpha.FeatureManager,
	extensionsManager *extensions.Manager,
	azd workflow.AzdCommandRunner,
) actions.Action {
	return &initAction{
		lazyAzdCtx:        lazyAzdCtx,
		lazyEnvManager:    lazyEnvManager,
		console:           console,
		cmdRun:            cmdRun,
		gitCli:            gitCli,
		flags:             flags,
		repoInitializer:   repoInitializer,
		templateManager:   templateManager,
		featuresManager:   featuresManager,
		extensionsManager: extensionsManager,
		azd:               azd,
	}
}

func (i *initAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting cwd: %w", err)
	}

	azdCtx := azdcontext.NewAzdContextWithDirectory(wd)
	i.lazyAzdCtx.SetValue(azdCtx)

	if i.flags.templateBranch != "" && i.flags.templatePath == "" {
		return nil,
			errors.New(
				"using branch argument (-b or --branch) requires a template argument (--template or -t) to be specified")
	}

	// ensure that git is available
	if err := tools.EnsureInstalled(ctx, []tools.ExternalTool{i.gitCli}...); err != nil {
		return nil, err
	}

	// Command title
	i.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Initializing an app to run on Azure (azd init)",
	})

	// AZD supports having .env at the root of the project directory as the initial environment file.
	// godotenv.Load() -> add all the values from the .env file in the process environment
	// If AZURE_ENV_NAME is set in the .env file, it will be used to name the environment during env initialize.
	if err := godotenv.Overload(); err != nil {
		// ignore the error if the file does not exist
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading .env file: %w", err)
		}
	}
	if i.flags.EnvFlag.EnvironmentName == "" ||
		(i.flags.EnvFlag.EnvironmentName != "" && !i.flags.EnvFlag.FromArg()) {
		// only azd init supports using .env to influence the command. The `-e` flag is linked to the
		// env var AZURE_ENV_NAME, which means it could've be set either from ENV or from arg.
		// re-setting the value here after loading the .env file overrides any value coming from the system env but
		// doest not override the value coming from the arg.
		i.flags.EnvFlag.EnvironmentName = os.Getenv(environment.EnvNameEnvVarName)
	}

	var existingProject bool
	if _, err := os.Stat(azdCtx.ProjectPath()); err == nil {
		existingProject = true
	} else if errors.Is(err, os.ErrNotExist) {
		existingProject = false
	} else {
		return nil, fmt.Errorf("checking if project exists: %w", err)
	}

	var initTypeSelect initType
	if i.flags.templatePath != "" || len(i.flags.templateTags) > 0 {
		// an explicit --template passed, always initialize from app template
		initTypeSelect = initAppTemplate
	}

	if i.flags.fromCode {
		if i.flags.templatePath != "" {
			return nil, errors.New("only one of init modes: --template, or --from-code should be set")
		}
		initTypeSelect = initFromApp
	}

	if i.flags.templatePath == "" && !i.flags.fromCode && existingProject {
		// only initialize environment when no mode is set explicitly
		initTypeSelect = initEnvironment
	}

	if initTypeSelect == initUnknown {
		initTypeSelect, err = promptInitType(i.console, ctx)
		if err != nil {
			return nil, err
		}
	}

	header := "New project initialized!"
	followUp := heredoc.Docf(`
	You can view the template code in your directory: %s
	Learn more about running 3rd party code on our DevHub: %s`,
		output.WithLinkFormat("%s", wd),
		output.WithLinkFormat("%s", "https://aka.ms/azd-third-party-code-notice"))

	   // If --tsp is set, ensure template is initialized first (if provided), then setup TypeScript infra
	   if i.flags.tsp {
			   if i.flags.templatePath != "" || len(i.flags.templateTags) > 0 {
					   // 1. Download template code
					   template, err := i.initializeTemplate(ctx, azdCtx)
					   if (err != nil) {
							   return nil, err
					   }
					   if _, err := i.initializeEnv(ctx, azdCtx, template.Metadata); err != nil {
							   return nil, err
					   }

					   // 2. If Bicep config exists, create deploy.ts in infra folder
					   infraDir := filepath.Join(azdCtx.ProjectDirectory(), "infra")
					   mainBicepPath := filepath.Join(infraDir, "main.bicep")
					   if _, err := os.Stat(mainBicepPath); err == nil {
							   deployTsPath := filepath.Join(infraDir, "deploy.ts")
							   if _, err := os.Stat(deployTsPath); os.IsNotExist(err) {
									   // Generate a default deploy.ts (could be improved to parse Bicep in future)
									   deployTsContent := `import { DefaultAzureCredential } from "@azure/identity";
import { ResourceManagementClient } from "@azure/arm-resources";
import * as fs from "fs";

const subscriptionId = process.env.AZURE_SUBSCRIPTION_ID!;
const resourceGroupName = process.env.AZURE_ENV_NAME!;
const location = process.env.AZURE_LOCATION!;

async function main() {
	const credential = new DefaultAzureCredential();
	const client = new ResourceManagementClient(credential, subscriptionId);
	await client.resourceGroups.createOrUpdate(resourceGroupName, { location });
	// Add more resources here as needed
	const outputs = {
		resourceGroupName: { value: resourceGroupName },
		location: { value: location },
	};
	fs.writeFileSync("outputs.json", JSON.stringify(outputs, null, 2));
	console.log(JSON.stringify(outputs));
}

main().catch(err => { console.error(err); process.exit(1); });
`
									   if err := os.WriteFile(deployTsPath, []byte(deployTsContent), 0644); err != nil {
											   return nil, fmt.Errorf("failed to write deploy.ts: %w", err)
									   }
							   }

							   // 3. Delete all files in infra except deploy.ts
							   entries, err := os.ReadDir(infraDir)
							   if err == nil {
									   for _, entry := range entries {
											   if entry.Name() != "deploy.ts" {
													   os.RemoveAll(filepath.Join(infraDir, entry.Name()))
											   }
									   }
							   }
					   }
			   }
			   // 4. Now setup TypeScript infra (updates azure.yaml, etc.)
			   err := initializeTypeScriptInfra(ctx, azdCtx)
			   if err != nil {
					   return nil, fmt.Errorf("failed to initialize TypeScript infra: %w", err)
			   }
			   header = "Initialized project with TypeScript infrastructure provider."
			   followUp = "You can now use azd with deploy.ts for infrastructure as code."
			   if i.flags.up {
					   // Optionally run azd up
					   startTime := time.Now()
					   i.azd.SetArgs([]string{"up", "--cwd", azdCtx.ProjectDirectory()})
					   err := i.azd.ExecuteContext(ctx)
					   header = "Project initialized and deployed with TypeScript provider in " + ux.DurationAsText(since(startTime)) + "."
					   if err != nil {
							   return nil, err
					   }
			   }
			   if err := i.initializeExtensions(ctx, azdCtx); err != nil {
					   return nil, fmt.Errorf("initializing project extensions: %w", err)
			   }
			   return &actions.ActionResult{
					   Message: &actions.ResultMessage{
							   Header:   header,
							   FollowUp: followUp,
					   },
			   }, nil
	   }

	   switch initTypeSelect {
	case initAppTemplate:
		tracing.SetUsageAttributes(fields.InitMethod.String("template"))
		template, err := i.initializeTemplate(ctx, azdCtx)
		if err != nil {
			return nil, err
		}

		if _, err := i.initializeEnv(ctx, azdCtx, template.Metadata); err != nil {
			return nil, err
		}

		if i.flags.up {
			// Prompt to deploy to Azure
			deploy, err := i.console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Do you want to run " + output.WithHighLightFormat("azd up") + " now?",
				DefaultValue: true,
				Help: "Template files have been initialized in your local directory. " +
					"If you want to provision and deploy now without making changes, select Y. If not, select N.",
			})
			if err != nil {
				return nil, err
			}

			if deploy {
				// Call azd up
				startTime := time.Now()
				i.azd.SetArgs([]string{"up", "--cwd", azdCtx.ProjectDirectory()})
				err := i.azd.ExecuteContext(ctx)
				header = "New project initialized! Provision and deploy to Azure was completed in " +
					ux.DurationAsText(since(startTime)) + "."
				if err != nil {
					return nil, err
				}
			}
		}

	case initFromApp:
		tracing.SetUsageAttributes(fields.InitMethod.String("app"))

		header = "Your app is ready for the cloud!"
		followUp = "You can provision and deploy your app to Azure by running the " + output.WithHighLightFormat("azd up") +
			" command in this directory. For more information on configuring your app, see " +
			output.WithHighLightFormat("./next-steps.md")
		entries, err := os.ReadDir(azdCtx.ProjectDirectory())
		if err != nil {
			return nil, fmt.Errorf("reading current directory: %w", err)
		}

		if len(entries) == 0 {
			return nil, &internal.ErrorWithSuggestion{
				Err: errors.New("no files found in the current directory"),
				Suggestion: "Ensure you're in the directory where your app code is located and try again." +
					" If you do not have code and would like to start with an app template, run '" +
					output.WithHighLightFormat("azd init") + "' and select the option to " +
					color.MagentaString("Use a template") + ".",
			}
		}

		err = i.repoInitializer.InitFromApp(ctx, azdCtx, func() (*environment.Environment, error) {
			return i.initializeEnv(ctx, azdCtx, templates.Metadata{})
		})
		if err != nil {
			return nil, err
		}
	case initEnvironment:
		env, err := i.initializeEnv(ctx, azdCtx, templates.Metadata{})
		if err != nil {
			return nil, err
		}

		header = fmt.Sprintf("Initialized environment %s.", env.Name())
		followUp = ""
	case initProject:
		tracing.SetUsageAttributes(fields.InitMethod.String("project"))

		composeAlphaEnabled := i.featuresManager.IsEnabled(composeFeature)
		if !composeAlphaEnabled {
			err = i.repoInitializer.InitializeMinimal(ctx, azdCtx)
			if err != nil {
				return nil, err
			}

			_, err := i.initializeEnv(ctx, azdCtx, templates.Metadata{})
			if err != nil {
				return nil, err
			}

			followUp = ""
		} else {
			fi, err := os.Stat(azdCtx.ProjectPath())
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}

			if fi != nil {
				return nil, fmt.Errorf("project already initialized")
			}

			name, err := i.console.Prompt(ctx, input.ConsoleOptions{
				Message:      "What is the name of your project?",
				DefaultValue: azdcontext.ProjectName(azdCtx.ProjectDirectory()),
			})
			if err != nil {
				return nil, err
			}

			prjConfig := project.ProjectConfig{
				Name: name,
			}

			if composeAlphaEnabled {
				prjConfig.MetaSchemaVersion = "alpha"
			}

			err = project.Save(ctx, &prjConfig, azdCtx.ProjectPath())
			if err != nil {
				return nil, fmt.Errorf("saving project config: %w", err)
			}

			followUp = "Run " + output.WithHighLightFormat("azd add") + " to add new Azure components to your project."
		}

		header = "Generated azure.yaml project file."
	default:
		panic("unhandled init type")
	}

	if err := i.initializeExtensions(ctx, azdCtx); err != nil {
		return nil, fmt.Errorf("initializing project extensions: %w", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   header,
			FollowUp: followUp,
		},
	}, nil
}

var composeFeature = alpha.MustFeatureKey("compose")

type initType int

const (
	initUnknown = iota
	initFromApp
	initAppTemplate
	initProject
	initEnvironment
)

func promptInitType(console input.Console, ctx context.Context) (initType, error) {
	selection, err := console.Select(ctx, input.ConsoleOptions{
		Message: "How do you want to initialize your app?",
		Options: []string{
			"Use code in the current directory",
			"Select a template",
			"Create a minimal project",
		},
	})
	if err != nil {
		return initUnknown, err
	}

	switch selection {
	case 0:
		return initFromApp, nil
	case 1:
		return initAppTemplate, nil
	case 2:
		return initProject, nil
	default:
		panic("unhandled selection")
	}
}

func (i *initAction) initializeTemplate(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext) (templates.Template, error) {
	err := i.repoInitializer.PromptIfNonEmpty(ctx, azdCtx)
	if err != nil {
		return templates.Template{}, err
	}

	var initFromTemplate *templates.Template
	if i.flags.templatePath == "" {
		// prompt for the template explicitly
		template, err := templates.PromptTemplate(
			ctx,
			"Select a project template:",
			i.templateManager,
			i.console,
			&templates.ListOptions{
				Tags: i.flags.templateTags,
			},
		)
		if err != nil {
			return templates.Template{}, err
		}

		initFromTemplate = &template
	} else {
		initFromTemplate = &templates.Template{
			RepositoryPath: i.flags.templatePath,
		}
	}

	err = i.repoInitializer.Initialize(ctx, azdCtx, initFromTemplate, i.flags.templateBranch)
	if err != nil {
		return templates.Template{}, fmt.Errorf("init from template repository: %w", err)
	}

	return *initFromTemplate, nil
}

func (i *initAction) initializeEnv(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	templateMetadata templates.Metadata) (*environment.Environment, error) {
	envName, err := azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, fmt.Errorf("retrieving default environment name: %w", err)
	}

	if envName != "" {
		return nil, environment.NewEnvironmentInitError(envName)
	}

	base := filepath.Base(azdCtx.ProjectDirectory())
	examples := []string{}
	for _, c := range []string{"dev", "test", "prod"} {
		suggest := environment.CleanName(base + "-" + c)
		if len(suggest) > environment.EnvironmentNameMaxLength {
			suggest = suggest[len(suggest)-environment.EnvironmentNameMaxLength:]
		}

		examples = append(examples, suggest)
	}

	// Environment manager requires azd context
	// Azd context isn't available in init so lazy instantiating
	// it here after the template is hydrated and the context is available
	envManager, err := i.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	envSpec := environment.Spec{
		Name:         i.flags.EnvironmentName,
		Subscription: i.flags.subscription,
		Location:     i.flags.location,
		Examples:     examples,
	}

	env, err := envManager.Create(ctx, envSpec)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	if err := azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: env.Name()}); err != nil {
		return nil, fmt.Errorf("saving default environment: %w", err)
	}

	// Copy template metadata into environment values
	for key, value := range templateMetadata.Variables {
		env.DotenvSet(key, value)
	}

	for key, value := range templateMetadata.Config {
		if err := env.Config.Set(key, value); err != nil {
			return nil, fmt.Errorf("setting environment config: %w", err)
		}
	}

	initialValuesFromEnv, err := repository.InitEnvFileValues()
	if err != nil {
		return nil, fmt.Errorf("loading initial env file values: %w", err)
	}
	for key, value := range initialValuesFromEnv {
		env.DotenvSet(key, value)
	}

	if err := envManager.Save(ctx, env); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return env, nil
}

// initializeExtensions installs extensions specified in the project config
func (i *initAction) initializeExtensions(ctx context.Context, azdCtx *azdcontext.AzdContext) error {
	if !i.featuresManager.IsEnabled(extensions.FeatureExtensions) {
		return nil
	}

	projectConfig, err := project.Load(ctx, azdCtx.ProjectPath())
	if err != nil {
		return fmt.Errorf("loading project config: %w", err)
	}

	// No extensions required
	if projectConfig.RequiredVersions == nil || len(projectConfig.RequiredVersions.Extensions) == 0 {
		return nil
	}

	installedExtensions, err := i.extensionsManager.ListInstalled()
	if err != nil {
		return fmt.Errorf("listing installed extensions: %w", err)
	}

	i.console.Message(ctx, "\nInstalling required extensions...")

	for extensionId, versionConstraint := range projectConfig.RequiredVersions.Extensions {
		stepMessage := fmt.Sprintf("Installing %s extension", output.WithHighLightFormat(extensionId))
		i.console.ShowSpinner(ctx, stepMessage, input.Step)

		installed, isInstalled := installedExtensions[extensionId]
		if isInstalled {
			stepMessage += output.WithGrayFormat(" (version %s already installed)", installed.Version)
			i.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		} else {
			installConstraint := "latest"
			if versionConstraint != nil {
				installConstraint = *versionConstraint
			}

			filterOptions := &extensions.FilterOptions{
				Version: installConstraint,
			}
			extensionVersion, err := i.extensionsManager.Install(ctx, extensionId, filterOptions)
			if err != nil {
				i.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return fmt.Errorf("installing extension %s: %w", extensionId, err)
			}

			stepMessage += output.WithGrayFormat(" (%s)", extensionVersion.Version)
			i.console.StopSpinner(ctx, stepMessage, input.StepDone)
		}
	}

	return nil
}

func getCmdInitHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription("Initialize a new application in your current directory.",
		[]string{
			formatHelpNote(
				fmt.Sprintf("Running %s without flags specified will prompt "+
					"you to initialize using your existing code, or from a template.",
					output.WithHighLightFormat("init"),
				)),
			formatHelpNote(
				"To view all available sample templates, including those submitted by the azd community, visit: " +
					output.WithLinkFormat("https://azure.github.io/awesome-azd") + "."),
		})
}

func getCmdInitHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Initialize a template to your current local directory from a GitHub repo.": fmt.Sprintf("%s %s",
			output.WithHighLightFormat("azd init --template"),
			output.WithWarningFormat("[GitHub repo URL]"),
		),
		"Initialize a template to your current local directory from a branch other than main.": fmt.Sprintf("%s %s %s %s",
			output.WithHighLightFormat("azd init --template"),
			output.WithWarningFormat("[GitHub repo URL]"),
			output.WithHighLightFormat("--branch"),
			output.WithWarningFormat("[Branch name]"),
		),
	})
}

