# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

<#
.SYNOPSIS
Analyzes GitHub issues by tags/labels for the Azure Developer CLI repository.

.DESCRIPTION
This script fetches and analyzes GitHub issues by their labels, providing statistics 
and insights to help with issue management and cleanup. It generates reports showing:
- Issue distribution by labels
- Top priority labels
- Issues without labels
- Stale issues
- Recommendations for issue cleanup

.PARAMETER RepoOwner
The GitHub repository owner (default: "Azure")

.PARAMETER RepoName
The GitHub repository name (default: "azure-dev")

.PARAMETER State
Filter issues by state: open, closed, or all (default: "open")

.PARAMETER OutputFormat
Output format: console, json, or csv (default: "console")

.PARAMETER OutputFile
Optional file path to save the analysis results

.PARAMETER AuthToken
GitHub personal access token for API authentication

.EXAMPLE
.\Analyze-GitHubIssues.ps1 -AuthToken "your-token"

.EXAMPLE
.\Analyze-GitHubIssues.ps1 -AuthToken "your-token" -OutputFormat "json" -OutputFile "issues-analysis.json"

.EXAMPLE
.\Analyze-GitHubIssues.ps1 -AuthToken "your-token" -State "all" -OutputFormat "csv" -OutputFile "all-issues.csv"
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$RepoOwner = "Azure",

    [Parameter(Mandatory = $false)]
    [string]$RepoName = "azure-dev",

    [Parameter(Mandatory = $false)]
    [ValidateSet("open", "closed", "all")]
    [string]$State = "open",

    [Parameter(Mandatory = $false)]
    [ValidateSet("console", "json", "csv")]
    [string]$OutputFormat = "console",

    [Parameter(Mandatory = $false)]
    [string]$OutputFile,

    [Parameter(Mandatory = $true)]
    [string]$AuthToken
)

# Import the GitHub API functions
. (Join-Path $PSScriptRoot ".." "common" "scripts" "Invoke-GitHubAPI.ps1")
. (Join-Path $PSScriptRoot ".." "common" "scripts" "common.ps1")

function Get-AllIssues {
    param(
        [string]$RepoOwner,
        [string]$RepoName,
        [string]$State,
        [string]$AuthToken
    )
    
    $allIssues = @()
    $page = 1
    $perPage = 100
    
    do {
        try {
            $uri = "https://api.github.com/repos/$RepoOwner/$RepoName/issues?state=$State&per_page=$perPage&page=$page"
            
            $headers = @{
                "Authorization" = "bearer $AuthToken"
                "Accept" = "application/vnd.github.v3+json"
            }
            
            $response = Invoke-RestMethod -Uri $uri -Headers $headers -Method GET -MaximumRetryCount 3
            
            if ($response.Count -eq 0) {
                break
            }
            
            # Filter out pull requests (they show up in issues API)
            $issues = $response | Where-Object { -not $_.pull_request }
            $allIssues += $issues
            
            Write-Host "Fetched page $page - $($issues.Count) issues"
            $page++
            
            # Rate limiting - wait a bit between requests
            Start-Sleep -Milliseconds 100
            
        } catch {
            Write-Error "Failed to fetch issues: $_"
            break
        }
    } while ($response.Count -eq $perPage)
    
    return $allIssues
}

function Analyze-IssuesByLabels {
    param(
        [array]$Issues
    )
    
    $labelStats = @{}
    $issuesWithoutLabels = @()
    $staleIssues = @()
    $priorityLabels = @("critical", "high", "medium", "low", "priority", "urgent", "blocker")
    $typeLabels = @("bug", "enhancement", "feature", "documentation", "question", "task")
    
    $thirtyDaysAgo = (Get-Date).AddDays(-30)
    $sixtyDaysAgo = (Get-Date).AddDays(-60)
    $ninetyDaysAgo = (Get-Date).AddDays(-90)
    
    foreach ($issue in $Issues) {
        $issueLabels = $issue.labels
        $lastUpdate = [DateTime]::Parse($issue.updated_at)
        
        # Track stale issues
        if ($lastUpdate -lt $ninetyDaysAgo) {
            $staleIssues += $issue
        }
        
        # Track issues without labels
        if ($issueLabels.Count -eq 0) {
            $issuesWithoutLabels += $issue
        }
        
        # Analyze labels
        foreach ($label in $issueLabels) {
            $labelName = $label.name
            if (-not $labelStats.ContainsKey($labelName)) {
                $labelStats[$labelName] = @{
                    Count = 0
                    Color = $label.color
                    Description = $label.description
                    Issues = @()
                }
            }
            $labelStats[$labelName].Count++
            $labelStats[$labelName].Issues += $issue
        }
    }
    
    # Sort labels by frequency
    $sortedLabels = $labelStats.GetEnumerator() | Sort-Object { $_.Value.Count } -Descending
    
    return @{
        TotalIssues = $Issues.Count
        LabelStats = $sortedLabels
        IssuesWithoutLabels = $issuesWithoutLabels
        StaleIssues = $staleIssues
        PriorityLabels = $priorityLabels
        TypeLabels = $typeLabels
        Analysis = @{
            MostCommonLabel = if ($sortedLabels.Count -gt 0) { $sortedLabels[0].Key } else { "None" }
            PercentageWithoutLabels = if ($Issues.Count -gt 0) { [Math]::Round(($issuesWithoutLabels.Count / $Issues.Count) * 100, 2) } else { 0 }
            PercentageStale = if ($Issues.Count -gt 0) { [Math]::Round(($staleIssues.Count / $Issues.Count) * 100, 2) } else { 0 }
        }
    }
}

function Format-ConsoleOutput {
    param(
        [hashtable]$Analysis
    )
    
    Write-Host "`n=== GitHub Issues Analysis ===" -ForegroundColor Green
    Write-Host "Repository: $RepoOwner/$RepoName" -ForegroundColor Gray
    Write-Host "State: $State" -ForegroundColor Gray
    Write-Host "Analysis Date: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')" -ForegroundColor Gray
    
    Write-Host "`n=== Summary ===" -ForegroundColor Cyan
    Write-Host "Total Issues: $($Analysis.TotalIssues)"
    Write-Host "Issues without Labels: $($Analysis.IssuesWithoutLabels.Count) ($($Analysis.Analysis.PercentageWithoutLabels)%)"
    Write-Host "Stale Issues (90+ days): $($Analysis.StaleIssues.Count) ($($Analysis.Analysis.PercentageStale)%)"
    Write-Host "Most Common Label: $($Analysis.Analysis.MostCommonLabel)"
    
    Write-Host "`n=== Top 10 Labels by Frequency ===" -ForegroundColor Cyan
    $top10Labels = $Analysis.LabelStats | Select-Object -First 10
    foreach ($label in $top10Labels) {
        $percentage = [Math]::Round(($label.Value.Count / $Analysis.TotalIssues) * 100, 1)
        Write-Host "  $($label.Key): $($label.Value.Count) issues ($percentage%)" -ForegroundColor White
        if ($label.Value.Description) {
            Write-Host "    Description: $($label.Value.Description)" -ForegroundColor Gray
        }
    }
    
    if ($Analysis.IssuesWithoutLabels.Count -gt 0) {
        Write-Host "`n=== Issues Without Labels (First 5) ===" -ForegroundColor Yellow
        $Analysis.IssuesWithoutLabels | Select-Object -First 5 | ForEach-Object {
            Write-Host "  #$($_.number): $($_.title)" -ForegroundColor White
            Write-Host "    Created: $($_.created_at) | Updated: $($_.updated_at)" -ForegroundColor Gray
        }
    }
    
    if ($Analysis.StaleIssues.Count -gt 0) {
        Write-Host "`n=== Stale Issues (First 5) ===" -ForegroundColor Red
        $Analysis.StaleIssues | Select-Object -First 5 | ForEach-Object {
            Write-Host "  #$($_.number): $($_.title)" -ForegroundColor White
            Write-Host "    Last Updated: $($_.updated_at)" -ForegroundColor Gray
        }
    }
    
    Write-Host "`n=== Recommendations ===" -ForegroundColor Magenta
    
    if ($Analysis.Analysis.PercentageWithoutLabels -gt 10) {
        Write-Host "⚠️  High percentage of issues without labels ($($Analysis.Analysis.PercentageWithoutLabels)%)"
        Write-Host "   Consider implementing a triage process to label new issues"
    }
    
    if ($Analysis.Analysis.PercentageStale -gt 15) {
        Write-Host "⚠️  High percentage of stale issues ($($Analysis.Analysis.PercentageStale)%)"
        Write-Host "   Consider reviewing and closing outdated issues"
    }
    
    if ($Analysis.TotalIssues -gt 500) {
        Write-Host "⚠️  Large number of open issues ($($Analysis.TotalIssues))"
        Write-Host "   Consider implementing issue lifecycle management"
    }
    
    # Recommendations based on label analysis
    $bugCount = ($Analysis.LabelStats | Where-Object { $_.Key -like "*bug*" } | Measure-Object -Property @{Expression = {$_.Value.Count}} -Sum).Sum
    $enhancementCount = ($Analysis.LabelStats | Where-Object { $_.Key -like "*enhancement*" -or $_.Key -like "*feature*" } | Measure-Object -Property @{Expression = {$_.Value.Count}} -Sum).Sum
    
    if ($bugCount -gt $enhancementCount * 2) {
        Write-Host "⚠️  High ratio of bugs to enhancements"
        Write-Host "   Consider focusing on bug fixes and code quality"
    }
    
    Write-Host "`n=== Issue Management Guidelines ===" -ForegroundColor Green
    Write-Host "1. Label new issues within 24 hours of creation"
    Write-Host "2. Review stale issues monthly and close if no longer relevant"
    Write-Host "3. Use consistent labeling conventions"
    Write-Host "4. Prioritize issues with priority labels"
    Write-Host "5. Consider milestones for feature planning"
    Write-Host "6. Archive or close issues older than 6 months with no activity"
}

function Export-Analysis {
    param(
        [hashtable]$Analysis,
        [string]$Format,
        [string]$FilePath
    )
    
    switch ($Format) {
        "json" {
            $Analysis | ConvertTo-Json -Depth 10 | Out-File -FilePath $FilePath -Encoding UTF8
            Write-Host "Analysis exported to JSON: $FilePath" -ForegroundColor Green
        }
        "csv" {
            # Export label statistics to CSV
            $csvData = $Analysis.LabelStats | ForEach-Object {
                [PSCustomObject]@{
                    Label = $_.Key
                    Count = $_.Value.Count
                    Percentage = [Math]::Round(($_.Value.Count / $Analysis.TotalIssues) * 100, 2)
                    Description = $_.Value.Description
                    Color = $_.Value.Color
                }
            }
            $csvData | Export-Csv -Path $FilePath -NoTypeInformation -Encoding UTF8
            Write-Host "Label statistics exported to CSV: $FilePath" -ForegroundColor Green
        }
    }
}

# Main execution
try {
    Write-Host "Starting GitHub Issues Analysis..." -ForegroundColor Green
    Write-Host "Repository: $RepoOwner/$RepoName" -ForegroundColor Gray
    Write-Host "State: $State" -ForegroundColor Gray
    
    # Fetch all issues
    Write-Host "`nFetching issues..." -ForegroundColor Yellow
    $issues = Get-AllIssues -RepoOwner $RepoOwner -RepoName $RepoName -State $State -AuthToken $AuthToken
    
    if ($issues.Count -eq 0) {
        Write-Host "No issues found." -ForegroundColor Yellow
        exit 0
    }
    
    Write-Host "Found $($issues.Count) issues" -ForegroundColor Green
    
    # Analyze issues
    Write-Host "`nAnalyzing issues..." -ForegroundColor Yellow
    $analysis = Analyze-IssuesByLabels -Issues $issues
    
    # Output results
    switch ($OutputFormat) {
        "console" {
            Format-ConsoleOutput -Analysis $analysis
        }
        "json" {
            if ($OutputFile) {
                Export-Analysis -Analysis $analysis -Format "json" -FilePath $OutputFile
            } else {
                $analysis | ConvertTo-Json -Depth 10
            }
        }
        "csv" {
            if ($OutputFile) {
                Export-Analysis -Analysis $analysis -Format "csv" -FilePath $OutputFile
            } else {
                Write-Error "CSV format requires -OutputFile parameter"
                exit 1
            }
        }
    }
    
    if ($OutputFormat -eq "console" -and $OutputFile) {
        Export-Analysis -Analysis $analysis -Format "json" -FilePath $OutputFile
    }
    
} catch {
    Write-Error "Analysis failed: $_"
    exit 1
}