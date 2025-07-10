# GitHub Issues Analysis Tools

This directory contains tools and guidelines for analyzing and managing GitHub issues in the Azure Developer CLI repository.

## Overview

The Azure Developer CLI repository has grown to have a large number of GitHub issues. These tools help analyze issue distribution by tags/labels and provide guidelines for ongoing issue management and cleanup.

## Tools

### 1. Issue Analysis Script
**Location**: `eng/scripts/Analyze-GitHubIssues.ps1`

PowerShell script that analyzes GitHub issues and provides detailed statistics:

- Issue distribution by labels
- Identification of unlabeled issues
- Detection of stale issues (90+ days without activity)
- Recommendations for issue management

**Usage**:
```powershell
# Basic analysis
.\eng\scripts\Analyze-GitHubIssues.ps1 -AuthToken "your-github-token"

# Export to JSON
.\eng\scripts\Analyze-GitHubIssues.ps1 -AuthToken "your-token" -OutputFormat "json" -OutputFile "analysis.json"

# Analyze all issues (open and closed)
.\eng\scripts\Analyze-GitHubIssues.ps1 -AuthToken "your-token" -State "all"
```

### 2. Test Suite
**Location**: `eng/scripts/Test-GitHubIssuesAnalysis.ps1`

Test script that validates the analysis functionality with mock data:

```powershell
.\eng\scripts\Test-GitHubIssuesAnalysis.ps1
```

### 3. GitHub Action Workflow
**Location**: `.github/workflows/analyze-issues.yml`

Automated workflow that can be:
- Triggered manually via GitHub UI
- Scheduled to run monthly
- Configured to output results in different formats

## Documentation

### Issue Management Guidelines
**Location**: `docs/github-issues-management.md`

Comprehensive guidelines covering:
- Labeling strategies and conventions
- Issue lifecycle management processes
- Cleanup procedures and best practices
- Automation opportunities
- Metrics and KPIs for issue management

## Quick Start

1. **Run Analysis**: Use the PowerShell script to get current issue statistics
2. **Review Guidelines**: Read the management guidelines document
3. **Set up Automation**: Configure the GitHub Action for regular analysis
4. **Implement Process**: Follow the recommended cleanup and triage processes

## Example Output

The analysis script provides output like:

```
=== GitHub Issues Analysis ===
Repository: Azure/azure-dev
State: open
Analysis Date: 2025-01-09 19:15:43

=== Summary ===
Total Issues: 816
Issues without Labels: 45 (5.5%)
Stale Issues (90+ days): 123 (15.1%)
Most Common Label: enhancement

=== Top 10 Labels by Frequency ===
  enhancement: 142 issues (17.4%)
  bug: 98 issues (12.0%)
  documentation: 67 issues (8.2%)
  feature: 54 issues (6.6%)
  ...

=== Recommendations ===
⚠️  High percentage of stale issues (15.1%)
   Consider reviewing and closing outdated issues
```

## Integration with Existing Tools

These tools build upon and integrate with existing GitHub API scripts in `eng/common/scripts/`:
- `Invoke-GitHubAPI.ps1` - Core GitHub API functions
- `Add-IssueLabels.ps1` - Label management
- `Remove-IssueLabel.ps1` - Label removal

## Requirements

- PowerShell 7.0+
- GitHub Personal Access Token with `issues:read` permission
- Access to Azure/azure-dev repository

## Contributing

To improve these tools:
1. Run the test suite before making changes
2. Update documentation when adding features
3. Follow PowerShell best practices
4. Test with different issue states and repositories

## Related Issues

- Original issue: #4445 - Analysis of GitHub issues by tag
- Addresses need for issue cleanup and management guidelines
- Supports ongoing issue triage and maintenance processes