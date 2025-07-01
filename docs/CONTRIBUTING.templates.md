# Contributing to templates stored in `azd`

## Pre-requisites

Language tooling:

- [NodeJS](https://nodejs.org/en/download/)
- [Python](https://www.python.org/downloads)
- [DotNet CLI](https://get.dot.net)

Infrastructure-as-code providers:

- [Bicep CLI](https://aka.ms/bicep-install)
- [Terraform](https://learn.hashicorp.com/tutorials/terraform/install-cli) (if working on Terraform templates)

Docker:

- [Docker](https://docs.docker.com/desktop/#download-and-install)

## Build templates

Unlike fully released [azd templates](https://github.com/topics/azd-templates), the template definition contained in this repository under the `templates` folder is disassembled into multiple directories. This allows re-use of specific assets such as Bicep files, resource assets, tests across templates. To reassemble the assets, a `repoman` file (`repo.yml`) is defined for each template that defines stitching and path rewriting via the `repoman generate` command. To learn more about the `repoman` tooling, see it's source under [generators/repo](../generators/repo).

The `templates` directory in this repository is structured as follows:

```yml
templates: # Root template folder
    todo: # Templates related to the Simple ToDo application
        common: # Common assets shared across templates.
        api: # API servers definition
        web: # Web servers definition
        projects: # The actual definition of how to assemble the template projects. repo.yaml is defined here which references relevant assets from  different directories. 
```

## Building

The simplest option is to build all templates in the repository:

```pwsh
./eng/scripts/Build-Templates.ps1
```

After this command completes, all generated templates can be found under the `.output` folder. `azd` commands can be ran in any of the generated folders for manual testing.

It is recommended to filter to specific templates for faster build times:

## Build templates with matching name

```pwsh
./eng/scripts/Build-Templates.ps1 todo-csharp-mongo
```

Builds the template named `todo-csharp-mongo`.

## Build templates with matching name regex

```pwsh
./eng/scripts/Build-Templates.ps1 python
```

Builds the template with names matching the regular expression `python`.

## Build templates under a specific directory

```pwsh
./eng/scripts/Build-Templates.ps1 -Path ./templates/todo/projects/python-mongo
```

Builds all templates discovered under `./templates/todo/projects/python-mongo`.

## Testing

### Manual Testing

Manual testing can be done by simply running `azd` commands in the generated template folders under `.output/<project>/generated`. Once you are happy with all the changes, submit a PR with your changes.

### Automated Testing

The repository includes comprehensive automated testing infrastructure for validating templates end-to-end. The testing system is located in [`templates/tests/`](../templates/tests/) and provides scripts for:

- **Full deployment testing**: Tests template initialization, provisioning, deployment, and cleanup
- **Playwright validation**: Runs automated smoke tests against deployed applications
- **Parallel execution**: Efficiently tests multiple templates simultaneously
- **Cleanup automation**: Automatically removes Azure resources and local files

#### Running Template Tests

To test a specific template:

```bash
cd templates/tests
./test-templates.sh -t "Azure-Samples/todo-nodejs-mongo"
```

To test all available templates:

```bash
cd templates/tests
./test-templates.sh
```

#### Test Configuration

The test scripts support various configuration options including:
- Custom Azure subscriptions and locations
- Different template branches
- Test-only mode (skip deployment)
- Custom environment naming
- Playwright test configuration

For detailed usage instructions, parameters, and examples, see the [Template Testing Documentation](../templates/tests/README.md).

#### Prerequisites for Automated Testing

- Azure CLI authenticated (`az login`)
- Azure Developer CLI installed (`azd`)
- Node.js for Playwright tests
- Valid Azure subscription with sufficient permissions

#### Adding Tests to New Templates

When creating new templates, include Playwright tests in a `/tests` directory within your template. See [`templates/todo/common/tests/README.md`](../templates/todo/common/tests/README.md) for an example of how to structure template tests.
