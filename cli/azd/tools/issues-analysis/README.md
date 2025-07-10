# GitHub Issues Analysis Tool

A comprehensive tool to analyze GitHub issues for the Azure Developer CLI repository and generate insights for the development team.

## Overview

This tool analyzes all GitHub issues in the Azure/azure-dev repository to identify:

- Top customer-reported issues and pain points
- Issue clustering and duplicate detection  
- Features already available vs. requested
- Documentation gaps for existing features
- Trend analysis over time
- Actionable recommendations

## Usage

Build and run the tool:

```bash
cd /home/runner/work/azure-dev/azure-dev/cli/azd/tools/issues-analysis
go build -o issues-analysis .
./issues-analysis analyze
```

### Command Line Options

```bash
./issues-analysis analyze [flags]

Flags:
  -r, --repo string            GitHub repository to analyze (default "Azure/azure-dev")
  -o, --output string          Output format (console, json, markdown) (default "console")
  -c, --include-closed         Include closed issues in analysis (default true)
  -l, --limit int              Limit number of issues to analyze (0 = all) (default 0)
  -h, --help                   help for analyze
```

### Output Formats

#### Console Output (default)
Human-readable console output with categorized sections.

#### JSON Output  
Machine-readable JSON format for further processing:
```bash
./issues-analysis analyze --output json > analysis-report.json
```

#### Markdown Output
Formatted markdown report suitable for documentation:
```bash
./issues-analysis analyze --output markdown > analysis-report.md
```

## Features

### 1. Top Customer Issues Analysis
- Ranks issues by engagement metrics (reactions + comments)
- Categorizes by type (Bug, Feature, Documentation, etc.)
- Calculates impact level (High, Medium, Low)
- Provides issue summaries and URLs

### 2. Issue Clustering
Groups related issues that may be addressing the same underlying problem:
- Authentication/Login issues
- Environment management problems
- Deployment failures
- Template/scaffolding issues
- CLI installation/setup problems
- VS Code extension issues
- Docker/container related issues
- Azure service integration problems

### 3. Existing Features Analysis
Identifies features that exist but users aren't aware of them:
- Compares feature requests against available documentation
- Identifies communication and discoverability gaps
- Provides links to existing documentation

### 4. Documentation Gap Analysis
Finds features with documentation that still generate support requests:
- Features with both docs AND open help requests
- Areas where documentation needs improvement
- Suggestions for better examples and tutorials

### 5. Trend Analysis
Analyzes issue patterns over time:
- Issue volume by month
- Growing issue categories
- Post-release patterns
- User adoption challenges

### 6. Actionable Recommendations
Provides prioritized next steps:
- HIGH priority bug fixes and documentation improvements
- MEDIUM priority UX enhancements
- LOW priority feature additions
- Quick wins for immediate impact

## Example Output

```
=== GitHub Issues Analysis Report ===

TOP 10 CUSTOMER ISSUES:
1. Support for multiple environments in azd - #1235
   Type: Feature | Reactions: 👍 20, ❤️ 3, 🚀 2 | Comments: 15
   Status: open | Impact: High
   URL: https://github.com/Azure/azure-dev/issues/1235

2. azd up fails with container deployment - #1237
   Type: Bug | Reactions: 👍 15, ❤️ 1, 🚀 2 | Comments: 12
   Status: open | Impact: High
   URL: https://github.com/Azure/azure-dev/issues/1237

ISSUE CLUSTERS:
- Authentication Issues (1 issues)
  - #1234: azd auth login fails with device code flow
  
- Environment Management (2 issues)
  - #1235: Support for multiple environments in azd
  - #1238: Environment variables not being passed to deployment

EXISTING FEATURES BEING REQUESTED:
- Support for multiple environments
  Requested in: [1235]
  Already exists: true
  Gap: Users unaware of feature / poor discoverability

DOCUMENTATION IMPROVEMENT OPPORTUNITIES:
- Custom Templates
  Problem: Documentation unclear about setup process
  Suggestion: Add step-by-step tutorial with examples

PRIORITY ACTIONS:
- HIGH: Fix authentication timeout issues (multiple reports)
- HIGH: Improve documentation for existing features users don't know about
- MEDIUM: Create better error messages for container deployment failures
- MEDIUM: Consolidate environment management issues and improve UX
- LOW: Add more template examples and tutorials
```

## Build Automation

Use the included Makefile for common tasks:

```bash
make build    # Build the tool
make run      # Build and run with default settings
make report   # Generate markdown report
make json     # Generate JSON report
make clean    # Clean build artifacts
make lint     # Lint the code
make help     # Show available targets
```

## Integration with GitHub

Currently, the tool uses mock data for demonstration. To integrate with real GitHub data, you can:

1. **Use GitHub CLI**: Leverage the existing GitHub CLI wrapper in the azd codebase
2. **Use GitHub API**: Make direct REST API calls to fetch issues
3. **Use GitHub GraphQL API**: For more efficient data fetching

## Extension Points

The tool is designed to be extensible:

- **Custom Analyzers**: Add new analysis functions for specific insights
- **Additional Outputs**: Support for HTML, PDF, or other formats
- **Real-time Updates**: Periodic analysis and trend monitoring
- **Integration**: Connect with project management tools or dashboards

## Dependencies

- Go 1.21+
- github.com/spf13/cobra for CLI interface
- Standard library packages for JSON, time, and string processing

## Future Enhancements

1. **Live GitHub Integration**: Replace mock data with real GitHub API calls
2. **Advanced Clustering**: Use machine learning for better issue grouping
3. **Sentiment Analysis**: Analyze issue tone and user frustration levels
4. **Automated Reports**: Generate periodic reports and email summaries
5. **Dashboard Integration**: Export data to monitoring dashboards
6. **Historical Analysis**: Track how issues and trends change over time