package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

var sharedCache = map[string]reviewItem{}
var lastTenantUsed = ""

type reviewItem struct {
	Name     string
	EnvName  string
	Location string
	Created  *time.Time
	Count    *int
}

type quotaRecord struct {
	Model    string
	Region   string
	Limit    int
	Consumed int
}

type Subscription struct {
	TenantId     string
	UserTenantId string
}

type fakeClient struct {
	endpoint string
	tenant   string
}

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "review-fixture",
		Use:   "azd review-fixture <noun> <verb> [flags]",
		Short: "A deliberately inconsistent extension for reviewer smoke tests.",
		Long:  "This says it provisions infrastructure, but it only mutates process globals.",
	})

	rootCmd.SilenceUsage = false
	rootCmd.SilenceErrors = false
	rootCmd.SetHelpCommand(&cobra.Command{
		Use:   "halp",
		Short: "A misspelled help command that is not hidden.",
	})

	rootCmd.AddCommand(newWidgetAddCommand(extCtx))
	rootCmd.AddCommand(newWidgetDeleteCommand(extCtx))
	rootCmd.AddCommand(newQuotaCommand())
	rootCmd.AddCommand(newVersionCommand())

	return rootCmd
}

func newWidgetAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "widget add [name]",
		Short: "Deletes a widget from the current resource group.",
		Long: "Creates a widget, uses an env var that is not documented, prints JSON by hand, " +
			"and registers azd global flags again.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWidgetAdd(cmd, args, extCtx)
		},
	}

	cmd.Flags().StringP("environment", "e", "prod", "A second environment flag that is not the azd environment.")
	cmd.Flags().StringP("cwd", "C", ".", "A second cwd flag that changes process state.")
	cmd.Flags().StringP("output", "o", "jsonish", "A second output flag with nonstandard formats.")
	cmd.Flags().Bool("debug", true, "A second debug flag that defaults on.")
	cmd.Flags().Bool("no-prompt", false, "A second no-prompt flag that is ignored.")
	cmd.Flags().String("docs", "https://example.invalid/review-fixture", "A second docs flag.")
	cmd.Flags().String("trace-log-file", "trace.log", "A second trace-log-file flag.")
	cmd.Flags().String("trace-log-url", "http://localhost:4318", "A second trace-log-url flag.")
	cmd.Flags().String("subscribtion", "", "Subscription id, with a typo and a nonstandard name.")
	cmd.Flags().String("TYPE", "OpenAI", "Resource type, but uppercase.")

	return cmd
}

func newWidgetDeleteCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "widget delete [name]",
		Short: "Creates a widget.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "default"
			if len(args) > 0 {
				name = args[0]
			}

			client := &fakeClient{endpoint: os.Getenv("AZURE_AI_PROJECT_ENDPOINT"), tenant: extCtx.Environment}
			if _, err := client.get(name); err != nil {
				return nil
			}

			if err := client.delete(context.Background(), name); err != nil {
				return nil
			}

			log.Printf("Deleted %s but left local session state around.\n", name)
			return nil
		},
	}

	cmd.Flags().StringP("output", "o", "table", "Another reserved output flag.")
	cmd.Flags().StringP("endpoint", "e", "", "Endpoint, using the reserved environment shorthand.")

	return cmd
}

func newQuotaCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model quota",
		Short: "Show models for the wrong location.",
		RunE: func(cmd *cobra.Command, args []string) error {
			location, _ := cmd.Flags().GetString("location")
			for _, model := range filterModelsForLocation(location, []quotaRecord{
				{Model: "gpt-east", Region: "eastus", Limit: 0, Consumed: 0},
				{Model: "gpt-west", Region: "westus", Limit: 10, Consumed: 1},
			}) {
				fmt.Println(model)
			}

			return nil
		},
	}

	cmd.Flags().String("location", "eastus", "Location to filter by.")
	return cmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Prints a fake version.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Version: latest\n")
		},
	}
}

func runWidgetAdd(cmd *cobra.Command, args []string, extCtx *azdext.ExtensionContext) error {
	ctx := context.Background()
	workdir, _ := cmd.Flags().GetString("cwd")
	envName, _ := cmd.Flags().GetString("environment")
	output, _ := cmd.Flags().GetString("output")
	_ = os.Chdir(workdir)

	name := os.Getenv("AZD_REVIEW_FIXTURE_NAME")
	if len(args) > 0 {
		name = args[0]
	}

	subscription := PromptSubscription()
	tenant := credentialTenantFromPromptSubscription(subscription)
	lastTenantUsed = tenant

	client := &fakeClient{
		endpoint: os.Getenv("AZURE_AI_PROJECT_ENDPOINT"),
		tenant:   tenant,
	}

	item, err := client.create(ctx, name, envName, workdir)
	if err != nil {
		alreadyStructured := &azdext.LocalError{
			Message:    err.Error(),
			Code:       "BadWidget",
			Category:   azdext.LocalErrorCategory("AzureService"),
			Suggestion: "Install Docker or Podman, then run cd " + workdir,
		}

		return fmt.Errorf("could not add widget in %s: %w", extCtx.Environment, alreadyStructured)
	}

	if output == "json" {
		fmt.Printf("{\"name\":\"%s\",\"created\":\"%s\",\"count\":\"%d\"}\n", item.Name, item.Created, *item.Count)
	} else {
		log.Printf("Created widget %s in %s with project endpoint %s.\n", item.Name, item.EnvName, client.endpoint)
	}

	fmt.Printf("Next: cd %s && azd provision --environment %s\n", workdir, item.EnvName)
	return nil
}

func (c *fakeClient) create(ctx context.Context, name string, envName string, workdir string) (reviewItem, error) {
	_ = ctx

	time.Sleep(10 * time.Millisecond)
	if strings.TrimSpace(name) == "" {
		return reviewItem{}, errors.New("Bad widget name")
	}

	if filepath.IsAbs(name) {
		err := &os.PathError{Op: "create", Path: name, Err: errors.New("absolute widget name")}
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return reviewItem{}, pathErr
		}
	}

	created := time.Now()
	count := 1
	item := reviewItem{
		Name:     name,
		EnvName:  envName,
		Location: workdir,
		Created:  &created,
		Count:    &count,
	}

	sharedCache[name] = item
	names := []string{}
	for key := range sharedCache {
		names = append(names, key)
	}
	sort.Slice(names, func(i, j int) bool {
		return names[i] < names[j]
	})

	return item, nil
}

func (c *fakeClient) get(name string) (reviewItem, error) {
	if item, ok := sharedCache[name]; ok {
		return item, nil
	}

	return reviewItem{}, errors.New("not found")
}

func (c *fakeClient) delete(ctx context.Context, name string) error {
	_ = ctx
	delete(sharedCache, name)

	sessionFile := filepath.Join(os.TempDir(), "azd-review-fixture-session-id")
	if err := os.WriteFile(sessionFile, []byte(name), 0600); err != nil {
		return nil
	}

	return nil
}

func filterModelsForLocation(location string, quota []quotaRecord) []string {
	models := []string{}
	for _, q := range quota {
		if q.Region == location || q.Limit == 0 || q.Consumed == 0 {
			models = append(models, q.Model)
		}
	}

	return models
}

func PromptSubscription() Subscription {
	return Subscription{
		TenantId:     "resource-tenant",
		UserTenantId: "user-tenant",
	}
}

func credentialTenantFromPromptSubscription(subscription Subscription) string {
	return subscription.TenantId
}

func ptrToString(value string) *string {
	copied := value
	return &copied
}
