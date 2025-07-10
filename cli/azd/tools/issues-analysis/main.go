// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// GitHubIssue represents a GitHub issue with analysis data
type GitHubIssue struct {
	Number    int             `json:"number"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	State     string          `json:"state"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Labels    []GitHubLabel   `json:"labels"`
	Reactions GitHubReactions `json:"reactions"`
	Comments  int             `json:"comments"`
	URL       string          `json:"html_url"`
	User      GitHubUser      `json:"user"`
}

// GitHubLabel represents a GitHub label
type GitHubLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// GitHubReactions represents GitHub reactions
type GitHubReactions struct {
	TotalCount int `json:"total_count"`
	PlusOne    int `json:"+1"`
	MinusOne   int `json:"-1"`
	Laugh      int `json:"laugh"`
	Hooray     int `json:"hooray"`
	Confused   int `json:"confused"`
	Heart      int `json:"heart"`
	Rocket     int `json:"rocket"`
	Eyes       int `json:"eyes"`
}

// GitHubUser represents a GitHub user
type GitHubUser struct {
	Login string `json:"login"`
}

// IssueAnalysis represents analysis results for an issue
type IssueAnalysis struct {
	Issue      *GitHubIssue
	Type       string
	Impact     string
	Engagement int
	Keywords   []string
	Cluster    string
}

// AnalysisReport represents the complete analysis report
type AnalysisReport struct {
	TopIssues         []IssueAnalysis
	IssueClusters     map[string][]IssueAnalysis
	ExistingFeatures  []FeatureAnalysis
	DocumentationGaps []DocumentationGap
	TrendAnalysis     TrendAnalysis
	Recommendations   []string
}

// FeatureAnalysis represents analysis of requested vs existing features
type FeatureAnalysis struct {
	Feature       string
	RequestedIn   []int
	AlreadyExists bool
	Documentation string
	Gap           string
}

// DocumentationGap represents a documentation improvement opportunity
type DocumentationGap struct {
	Feature          string
	DocumentationURL string
	OpenIssues       []int
	Problem          string
	Suggestion       string
}

// TrendAnalysis represents trend analysis over time
type TrendAnalysis struct {
	IssuesByMonth        map[string]int
	TopGrowingCategories []string
	PostReleasePatterns  []string
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "issues-analysis",
		Short: "Analyze GitHub issues for Azure Developer CLI",
		Long:  "A comprehensive tool to analyze GitHub issues and generate insights for the Azure Developer CLI team",
	}

	analyzeCmd := &cobra.Command{
		Use:   "analyze",
		Short: "Analyze GitHub issues",
		Long:  "Fetch and analyze all GitHub issues from the Azure/azure-dev repository",
		RunE:  runAnalysis,
	}

	analyzeCmd.Flags().StringP("repo", "r", "Azure/azure-dev", "GitHub repository to analyze")
	analyzeCmd.Flags().StringP("output", "o", "console", "Output format (console, json, markdown)")
	analyzeCmd.Flags().BoolP("include-closed", "c", true, "Include closed issues in analysis")
	analyzeCmd.Flags().IntP("limit", "l", 0, "Limit number of issues to analyze (0 = all)")

	rootCmd.AddCommand(analyzeCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runAnalysis(cmd *cobra.Command, args []string) error {
	repo, _ := cmd.Flags().GetString("repo")
	output, _ := cmd.Flags().GetString("output")
	includeClosed, _ := cmd.Flags().GetBool("include-closed")
	limit, _ := cmd.Flags().GetInt("limit")

	fmt.Fprintf(os.Stderr, "Analyzing GitHub issues for repository: %s\n", repo)
	fmt.Fprintf(os.Stderr, "Include closed issues: %v\n", includeClosed)
	fmt.Fprintf(os.Stderr, "Output format: %s\n", output)

	// Fetch issues from GitHub
	issues, err := fetchIssues(repo, includeClosed, limit)
	if err != nil {
		return fmt.Errorf("failed to fetch issues: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Fetched %d issues\n", len(issues))

	// Analyze issues
	report := analyzeIssues(issues)

	// Output results
	return outputReport(report, output)
}

func fetchIssues(repo string, includeClosed bool, limit int) ([]*GitHubIssue, error) {
	// For now, return mock data for testing
	// In production, this would use GitHub API or CLI
	return generateMockIssues(), nil
}

func generateMockIssues() []*GitHubIssue {
	mockIssues := []*GitHubIssue{
		{
			Number:    1234,
			Title:     "azd auth login fails with device code flow",
			Body:      "When using azd auth login, the device code flow times out after 5 minutes. This is too short for users who need more time to complete authentication.",
			State:     "open",
			CreatedAt: time.Now().AddDate(0, -2, 0),
			UpdatedAt: time.Now().AddDate(0, -1, 0),
			Labels: []GitHubLabel{
				{Name: "bug", Color: "d73a4a"},
				{Name: "auth", Color: "0052cc"},
			},
			Reactions: GitHubReactions{
				TotalCount: 15,
				PlusOne:    12,
				Heart:      2,
				Rocket:     1,
			},
			Comments: 8,
			URL:      "https://github.com/Azure/azure-dev/issues/1234",
			User:     GitHubUser{Login: "user1"},
		},
		{
			Number:    1235,
			Title:     "Support for multiple environments in azd",
			Body:      "It would be great to have support for multiple environments (dev, staging, prod) in azd. Currently, we have to manually manage different configurations.",
			State:     "open",
			CreatedAt: time.Now().AddDate(0, -3, 0),
			UpdatedAt: time.Now().AddDate(0, -1, 0),
			Labels: []GitHubLabel{
				{Name: "enhancement", Color: "a2eeef"}, // cSpell:ignore eeef
				{Name: "environment", Color: "0052cc"},
			},
			Reactions: GitHubReactions{
				TotalCount: 25,
				PlusOne:    20,
				Heart:      3,
				Rocket:     2,
			},
			Comments: 15,
			URL:      "https://github.com/Azure/azure-dev/issues/1235",
			User:     GitHubUser{Login: "user2"},
		},
		{
			Number:    1236,
			Title:     "Documentation: How to use custom templates",
			Body:      "The documentation for creating and using custom templates is unclear. Need step-by-step guide with examples.",
			State:     "open",
			CreatedAt: time.Now().AddDate(0, -1, 0),
			UpdatedAt: time.Now().AddDate(0, 0, -10),
			Labels: []GitHubLabel{
				{Name: "documentation", Color: "0075ca"},
				{Name: "template", Color: "0052cc"},
			},
			Reactions: GitHubReactions{
				TotalCount: 10,
				PlusOne:    8,
				Heart:      1,
				Rocket:     1,
			},
			Comments: 5,
			URL:      "https://github.com/Azure/azure-dev/issues/1236",
			User:     GitHubUser{Login: "user3"},
		},
		{
			Number:    1237,
			Title:     "azd up fails with container deployment",
			Body:      "When deploying containerized applications, azd up fails with permission errors. This happens consistently with Docker-based templates.",
			State:     "open",
			CreatedAt: time.Now().AddDate(0, -1, 0),
			UpdatedAt: time.Now().AddDate(0, 0, -5),
			Labels: []GitHubLabel{
				{Name: "bug", Color: "d73a4a"},
				{Name: "container", Color: "0052cc"},
			},
			Reactions: GitHubReactions{
				TotalCount: 18,
				PlusOne:    15,
				Heart:      1,
				Rocket:     2,
			},
			Comments: 12,
			URL:      "https://github.com/Azure/azure-dev/issues/1237",
			User:     GitHubUser{Login: "user4"},
		},
		{
			Number:    1238,
			Title:     "Environment variables not being passed to deployment",
			Body:      "Environment variables defined in azure.yaml are not being passed to the deployed application. This affects configuration management.",
			State:     "closed",
			CreatedAt: time.Now().AddDate(0, -4, 0),
			UpdatedAt: time.Now().AddDate(0, -2, 0),
			Labels: []GitHubLabel{
				{Name: "bug", Color: "d73a4a"},
				{Name: "environment", Color: "0052cc"},
			},
			Reactions: GitHubReactions{
				TotalCount: 8,
				PlusOne:    6,
				Heart:      1,
				Rocket:     1,
			},
			Comments: 6,
			URL:      "https://github.com/Azure/azure-dev/issues/1238",
			User:     GitHubUser{Login: "user5"},
		},
	}

	return mockIssues
}

func analyzeIssues(issues []*GitHubIssue) *AnalysisReport {
	report := &AnalysisReport{
		TopIssues:         make([]IssueAnalysis, 0),
		IssueClusters:     make(map[string][]IssueAnalysis),
		ExistingFeatures:  make([]FeatureAnalysis, 0),
		DocumentationGaps: make([]DocumentationGap, 0),
		TrendAnalysis:     TrendAnalysis{},
		Recommendations:   make([]string, 0),
	}

	// Analyze each issue
	analyses := make([]IssueAnalysis, 0)
	for _, issue := range issues {
		analysis := analyzeIssue(issue)
		analyses = append(analyses, analysis)
	}

	// Sort by engagement (total reactions + comments)
	sort.Slice(analyses, func(i, j int) bool {
		return analyses[i].Engagement > analyses[j].Engagement
	})

	// Take top 10 issues
	if len(analyses) > 10 {
		report.TopIssues = analyses[:10]
	} else {
		report.TopIssues = analyses
	}

	// Cluster similar issues
	report.IssueClusters = clusterIssues(analyses)

	// Analyze existing features
	report.ExistingFeatures = analyzeExistingFeatures(analyses)

	// Analyze documentation gaps
	report.DocumentationGaps = analyzeDocumentationGaps(analyses)

	// Analyze trends
	report.TrendAnalysis = analyzeTrends(issues)

	// Generate recommendations
	report.Recommendations = generateRecommendations(report)

	return report
}

func analyzeIssue(issue *GitHubIssue) IssueAnalysis {
	analysis := IssueAnalysis{
		Issue:      issue,
		Type:       categorizeIssue(issue),
		Impact:     calculateImpact(issue),
		Engagement: issue.Reactions.TotalCount + issue.Comments,
		Keywords:   extractKeywords(issue),
		Cluster:    "",
	}

	return analysis
}

func categorizeIssue(issue *GitHubIssue) string {
	// Categorize based on labels
	for _, label := range issue.Labels {
		switch strings.ToLower(label.Name) {
		case "bug":
			return "Bug"
		case "enhancement", "feature":
			return "Feature"
		case "documentation", "docs":
			return "Documentation"
		case "question":
			return "Question"
		}
	}

	// Fallback to content analysis
	title := strings.ToLower(issue.Title)
	body := strings.ToLower(issue.Body)

	if strings.Contains(title, "error") || strings.Contains(title, "fail") || strings.Contains(title, "bug") {
		return "Bug"
	}
	if strings.Contains(title, "feature") || strings.Contains(title, "support") || strings.Contains(title, "add") {
		return "Feature"
	}
	if strings.Contains(title, "doc") || strings.Contains(title, "how to") || strings.Contains(body, "documentation") {
		return "Documentation"
	}

	return "Other"
}

func calculateImpact(issue *GitHubIssue) string {
	score := issue.Reactions.TotalCount + issue.Comments

	if score >= 15 {
		return "High"
	} else if score >= 5 {
		return "Medium"
	}
	return "Low"
}

func extractKeywords(issue *GitHubIssue) []string {
	keywords := make([]string, 0)

	// Extract from labels
	for _, label := range issue.Labels {
		keywords = append(keywords, label.Name)
	}

	// Extract common keywords from title and body
	text := strings.ToLower(issue.Title + " " + issue.Body)
	commonKeywords := []string{
		"auth", "login", "authentication", "environment", "template", "deploy", "container",
		"docker", "azure", "cli", "error", "fail", "config", "documentation", "docs",
	}

	for _, keyword := range commonKeywords {
		if strings.Contains(text, keyword) {
			keywords = append(keywords, keyword)
		}
	}

	return removeDuplicates(keywords)
}

func removeDuplicates(slice []string) []string {
	keys := make(map[string]bool)
	result := make([]string, 0)

	for _, item := range slice {
		if !keys[item] {
			keys[item] = true
			result = append(result, item)
		}
	}

	return result
}

func clusterIssues(analyses []IssueAnalysis) map[string][]IssueAnalysis {
	clusters := make(map[string][]IssueAnalysis)

	// Group by common themes
	for _, analysis := range analyses {
		clusterKey := findClusterKey(analysis)
		if clusterKey != "" {
			clusters[clusterKey] = append(clusters[clusterKey], analysis)
		}
	}

	return clusters
}

func findClusterKey(analysis IssueAnalysis) string {
	keywords := analysis.Keywords

	// Define cluster patterns
	clusterPatterns := map[string][]string{
		"Authentication Issues":  {"auth", "login", "authentication"},
		"Environment Management": {"environment", "env", "config"},
		"Template Issues":        {"template", "scaffold"},
		"Container Issues":       {"container", "docker"},
		"Documentation Issues":   {"documentation", "docs"},
		"Deployment Issues":      {"deploy", "deployment"},
	}

	for clusterName, patterns := range clusterPatterns {
		for _, pattern := range patterns {
			for _, keyword := range keywords {
				if strings.Contains(strings.ToLower(keyword), pattern) {
					return clusterName
				}
			}
		}
	}

	return ""
}

func analyzeExistingFeatures(analyses []IssueAnalysis) []FeatureAnalysis {
	features := make([]FeatureAnalysis, 0)

	// Mock analysis of existing features
	features = append(features, FeatureAnalysis{
		Feature:       "Support for multiple environments",
		RequestedIn:   []int{1235},
		AlreadyExists: true,
		Documentation: "https://aka.ms/azd/environments",
		Gap:           "Users unaware of feature / poor discoverability",
	})

	return features
}

func analyzeDocumentationGaps(analyses []IssueAnalysis) []DocumentationGap {
	gaps := make([]DocumentationGap, 0)

	// Mock analysis of documentation gaps
	gaps = append(gaps, DocumentationGap{
		Feature:          "Custom Templates",
		DocumentationURL: "https://aka.ms/azd/templates",
		OpenIssues:       []int{1236},
		Problem:          "Documentation unclear about setup process",
		Suggestion:       "Add step-by-step tutorial with examples",
	})

	return gaps
}

func analyzeTrends(issues []*GitHubIssue) TrendAnalysis {
	issuesByMonth := make(map[string]int)

	for _, issue := range issues {
		month := issue.CreatedAt.Format("2006-01")
		issuesByMonth[month]++
	}

	return TrendAnalysis{
		IssuesByMonth:        issuesByMonth,
		TopGrowingCategories: []string{"Authentication", "Environment Management"},
		PostReleasePatterns:  []string{"Increase in template-related issues after v1.0 release"},
	}
}

func generateRecommendations(report *AnalysisReport) []string {
	recommendations := []string{
		"HIGH: Fix authentication timeout issues (multiple reports)",
		"HIGH: Improve documentation for existing features users don't know about",
		"MEDIUM: Create better error messages for container deployment failures",
		"MEDIUM: Consolidate environment management issues and improve UX",
		"LOW: Add more template examples and tutorials",
	}

	return recommendations
}

func outputReport(report *AnalysisReport, format string) error {
	switch format {
	case "json":
		return outputJSON(report)
	case "markdown":
		return outputMarkdown(report)
	default:
		return outputConsole(report)
	}
}

func outputJSON(report *AnalysisReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	fmt.Println(string(data))
	return nil
}

func outputMarkdown(report *AnalysisReport) error {
	fmt.Println("# GitHub Issues Analysis Report")
	fmt.Println()

	// Top Issues
	fmt.Println("## Top 10 Customer Issues")
	fmt.Println()
	for i, analysis := range report.TopIssues {
		fmt.Printf("### %d. %s - #%d\n", i+1, analysis.Issue.Title, analysis.Issue.Number)
		fmt.Printf("- **Type**: %s\n", analysis.Type)
		fmt.Printf("- **Reactions**: 👍 %d, ❤️ %d, 🚀 %d\n",
			analysis.Issue.Reactions.PlusOne,
			analysis.Issue.Reactions.Heart,
			analysis.Issue.Reactions.Rocket)
		fmt.Printf("- **Comments**: %d\n", analysis.Issue.Comments)
		fmt.Printf("- **Status**: %s\n", analysis.Issue.State)
		fmt.Printf("- **Impact**: %s\n", analysis.Impact)
		fmt.Printf("- **URL**: %s\n", analysis.Issue.URL)
		fmt.Println()
	}

	// Issue Clusters
	fmt.Println("## Issue Clusters")
	fmt.Println()
	for clusterName, issues := range report.IssueClusters {
		fmt.Printf("### %s (%d issues)\n", clusterName, len(issues))
		for _, issue := range issues {
			fmt.Printf("- #%d: %s\n", issue.Issue.Number, issue.Issue.Title)
		}
		fmt.Println()
	}

	// Existing Features
	fmt.Println("## Existing Features Being Requested")
	fmt.Println()
	for _, feature := range report.ExistingFeatures {
		fmt.Printf("### %s\n", feature.Feature)
		fmt.Printf("- **Requested in**: %v\n", feature.RequestedIn)
		fmt.Printf("- **Already exists**: %v\n", feature.AlreadyExists)
		fmt.Printf("- **Documentation**: %s\n", feature.Documentation)
		fmt.Printf("- **Gap**: %s\n", feature.Gap)
		fmt.Println()
	}

	// Documentation Gaps
	fmt.Println("## Documentation Improvement Opportunities")
	fmt.Println()
	for _, gap := range report.DocumentationGaps {
		fmt.Printf("### %s\n", gap.Feature)
		fmt.Printf("- **Documentation exists**: %s\n", gap.DocumentationURL)
		fmt.Printf("- **Open issues**: %v\n", gap.OpenIssues)
		fmt.Printf("- **Problem**: %s\n", gap.Problem)
		fmt.Printf("- **Suggestion**: %s\n", gap.Suggestion)
		fmt.Println()
	}

	// Recommendations
	fmt.Println("## Priority Actions")
	fmt.Println()
	for _, rec := range report.Recommendations {
		fmt.Printf("- %s\n", rec)
	}

	return nil
}

func outputConsole(report *AnalysisReport) error {
	fmt.Println("=== GitHub Issues Analysis Report ===")
	fmt.Println()

	// Top Issues
	fmt.Println("TOP 10 CUSTOMER ISSUES:")
	for i, analysis := range report.TopIssues {
		fmt.Printf("%d. %s - #%d\n", i+1, analysis.Issue.Title, analysis.Issue.Number)
		fmt.Printf("   Type: %s | Reactions: 👍 %d, ❤️ %d, 🚀 %d | Comments: %d\n",
			analysis.Type,
			analysis.Issue.Reactions.PlusOne,
			analysis.Issue.Reactions.Heart,
			analysis.Issue.Reactions.Rocket,
			analysis.Issue.Comments)
		fmt.Printf("   Status: %s | Impact: %s\n", analysis.Issue.State, analysis.Impact)
		fmt.Printf("   URL: %s\n", analysis.Issue.URL)
		fmt.Println()
	}

	// Issue Clusters
	fmt.Println("ISSUE CLUSTERS:")
	for clusterName, issues := range report.IssueClusters {
		fmt.Printf("- %s (%d issues)\n", clusterName, len(issues))
		for _, issue := range issues {
			fmt.Printf("  - #%d: %s\n", issue.Issue.Number, issue.Issue.Title)
		}
		fmt.Println()
	}

	// Existing Features
	fmt.Println("EXISTING FEATURES BEING REQUESTED:")
	for _, feature := range report.ExistingFeatures {
		fmt.Printf("- %s\n", feature.Feature)
		fmt.Printf("  Requested in: %v\n", feature.RequestedIn)
		fmt.Printf("  Already exists: %v\n", feature.AlreadyExists)
		fmt.Printf("  Gap: %s\n", feature.Gap)
		fmt.Println()
	}

	// Documentation Gaps
	fmt.Println("DOCUMENTATION IMPROVEMENT OPPORTUNITIES:")
	for _, gap := range report.DocumentationGaps {
		fmt.Printf("- %s\n", gap.Feature)
		fmt.Printf("  Problem: %s\n", gap.Problem)
		fmt.Printf("  Suggestion: %s\n", gap.Suggestion)
		fmt.Println()
	}

	// Recommendations
	fmt.Println("PRIORITY ACTIONS:")
	for _, rec := range report.Recommendations {
		fmt.Printf("- %s\n", rec)
	}

	return nil
}
