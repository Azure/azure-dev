// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"azure.ai.training/internal/utils"
	"azure.ai.training/pkg/client"
	"azure.ai.training/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// proxyEndpointPattern blocks shell metacharacters that could enable command injection
// when the endpoint is embedded in an SSH ProxyCommand string.
var proxyEndpointPattern = regexp.MustCompile(`^(wss?|https?)://[a-zA-Z0-9_.:/\-@%?=+,\[\]~]+$`)

func newJobConnectSSHCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	var name string
	var nodeIndex int
	var privateKeyFile string

	cmd := &cobra.Command{
		Use:   "connect-ssh",
		Short: "Open an SSH session to a node of a running training job",
		Long: "Open an SSH session to the container of a running training job, optionally targeting a specific node by index.\n\n" +
			"Prerequisite: the job must have been submitted with an SSH service enabled in its YAML, e.g.:\n\n" +
			"  services:\n" +
			"    my_ssh:\n" +
			"      type: ssh\n" +
			"      ssh_public_keys: |\n" +
			"        ssh-ed25519 AAAA... user@host\n\n" +
			"The SSH service must reach 'Running' status before this command can connect; " +
			"that typically takes 30s\u2013120s after the job enters Running. " +
			"If --private-key-file-path is omitted, the OpenSSH client falls back to the default identities under ~/.ssh/.\n\n" +
			"Example:\n" +
			"  azd ai training job connect-ssh --name my-job --node-index 0 --private-key-file-path ~/.ssh/id_ed25519",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if nodeIndex < 0 {
				return fmt.Errorf("--node-index must be >= 0")
			}

			// Verify ssh client is available before doing any network calls
			sshPath, err := lookupSSHBinary()
			if err != nil {
				return err
			}

			apiClient, err := buildJobAPIClient(ctx)
			if err != nil {
				return err
			}
			apiClient.SetDebugBody(extCtx.Debug)

			// 1. Get job → tracking endpoint
			job, err := apiClient.GetJob(ctx, name)
			if err != nil {
				return fmt.Errorf("failed to get job %q: %w", name, err)
			}

			// Refuse early if the job is in a terminal state — the container is gone
			// and any tunnel attempt will time out with a confusing TCP error.
			switch strings.ToLower(job.Properties.Status) {
			case "completed", "failed", "canceled", "cancelled", "notresponding":
				return fmt.Errorf(
					"job %q is in terminal state %q; SSH is only available while the job is Running",
					name, job.Properties.Status)
			}

			trackingEndpoint := utils.ServiceEndpoint(job.Properties.Services, "Tracking")
			if trackingEndpoint == "" {
				return fmt.Errorf("job %q has no tracking endpoint yet; ensure the job has started", name)
			}

			// 2. Get service instance for the requested node
			instance, err := apiClient.GetServiceInstance(ctx, trackingEndpoint, name, nodeIndex)
			if err != nil {
				return fmt.Errorf("failed to get service instance for node %d: %w", nodeIndex, err)
			}

			// 3. Validate per the four error scenarios (mirrors AML CLI behavior)
			proxyEndpoint, err := resolveSSHProxyEndpoint(instance, nodeIndex)
			if err != nil {
				return err
			}

			// 4. Validate proxy endpoint to prevent command injection in ProxyCommand
			if !proxyEndpointPattern.MatchString(proxyEndpoint) {
				return fmt.Errorf("proxy endpoint contains invalid characters; refusing to launch ssh")
			}

			// 5. Locate our own binary so we can use it as ProxyCommand
			selfPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to locate azd extension binary: %w", err)
			}

			// 6. Build and exec ssh
			return runSSH(ctx, sshPath, selfPath, proxyEndpoint, privateKeyFile, extCtx.Debug)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Job name (required)")
	cmd.Flags().IntVar(&nodeIndex, "node-index", 0,
		"Zero-based index of the node to connect to in a multi-node job (default 0)")
	cmd.Flags().StringVar(&privateKeyFile, "private-key-file-path", "",
		"Path to the SSH private key file (optional; ssh will use ~/.ssh defaults if omitted)")

	return cmd
}

// lookupSSHBinary returns the path to the ssh client. On Windows, prefers the
// 64-bit OpenSSH location to avoid the 32-bit System32 redirector issue.
func lookupSSHBinary() (string, error) {
	if runtime.GOOS == "windows" {
		systemRoot := os.Getenv("SystemRoot")
		if systemRoot != "" {
			candidate := filepath.Join(systemRoot, "System32", "OpenSSH", "ssh.exe")
			// #nosec G304 G703 -- candidate is derived from %SystemRoot%, not user input
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	path, err := exec.LookPath("ssh")
	if err != nil {
		return "", fmt.Errorf("ssh client not found in PATH; please install OpenSSH client")
	}
	return path, nil
}

// resolveSSHProxyEndpoint inspects the service instance response and returns the
// SSH ProxyEndpoint URL, or a clear error matching one of the four failure modes.
func resolveSSHProxyEndpoint(instance *models.ServiceInstance, nodeIndex int) (string, error) {
	// Scenario 1: No services on the node
	if instance == nil || len(instance.Instances) == 0 {
		return "", fmt.Errorf(
			"the node %d of the job does not have services; ensure that the job has services",
			nodeIndex,
		)
	}

	// Scenario 2: Services exist but none are SSH (job not ssh-enabled)
	var ssh *models.ServiceInstanceDetail
	for _, svc := range instance.Instances {
		if svc.Type == "SSH" {
			s := svc
			ssh = &s
			break
		}
	}
	if ssh == nil {
		return "", fmt.Errorf(
			"please ensure that the job is ssh enabled on node '%d'", nodeIndex)
	}

	// Scenario 3: SSH exists but not Running
	if ssh.Status != "Running" {
		return "", fmt.Errorf(
			"please ensure that ssh service at node '%d' has the status as 'Running'. The current status is '%s'",
			nodeIndex, ssh.Status)
	}

	// Scenario 4: Running but missing ProxyEndpoint
	if ssh.Properties == nil {
		return "", fmt.Errorf("the ssh JobService.properties is missing ProxyEndpoint")
	}
	endpointAny, ok := ssh.Properties["ProxyEndpoint"]
	if !ok {
		return "", fmt.Errorf("the ssh JobService.properties is missing ProxyEndpoint")
	}
	endpoint, ok := endpointAny.(string)
	if !ok || endpoint == "" {
		return "", fmt.Errorf("the ssh JobService.properties is missing ProxyEndpoint")
	}
	return endpoint, nil
}

// runSSH constructs and executes the ssh command with our binary as ProxyCommand.
func runSSH(ctx context.Context, sshPath, selfPath, proxyEndpoint, privateKeyFile string, debug bool) error {
	// ProxyCommand: ssh executes our binary, which opens the WebSocket and pipes stdio.
	// Quote the binary path to handle spaces; the proxy endpoint is passed verbatim.
	// The extension binary's root command is "training" (azd strips the
	// "ai training" prefix when dispatching), so the path here is just
	// "job _ssh-proxy <url>".
	//
	// OpenSSH performs percent-token expansion on the ProxyCommand value
	// (e.g. %h, %p, %r). Escape literal '%' in the URL as '%%' so URL
	// percent-encoded segments survive without being rewritten by ssh.
	escapedEndpoint := strings.ReplaceAll(proxyEndpoint, "%", "%%")
	proxyCmd := fmt.Sprintf(`"%s" job _ssh-proxy %s`, selfPath, escapedEndpoint)

	// The destination is just a label when ProxyCommand is used (the real TCP comes from
	// the proxy). Use a fixed alias so ssh doesn't try to parse the proxy URL as host:port.
	const destAlias = "azureml-job"

	args := []string{
		"-o", "ProxyCommand=" + proxyCmd,
		// Disable StrictHostKeyChecking since the proxy endpoint hostname rotates per session
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + os.DevNull,
	}
	if privateKeyFile != "" {
		args = append(args, "-i", privateKeyFile)
	}
	if debug {
		args = append(args, "-vvv")
		fmt.Fprintf(os.Stderr, "DEBUG: ssh %s azureuser@%s\n", strings.Join(args, " "), destAlias)
	}
	args = append(args, "azureuser@"+destAlias)

	// #nosec G204 -- sshPath is resolved by lookupSSHBinary; args are constructed from validated proxy endpoint
	sshCmd := exec.CommandContext(ctx, sshPath, args...)
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr

	if err := sshCmd.Run(); err != nil {
		// Pass ssh's exit code through transparently so callers/scripts can
		// react to it. ssh already prints its own diagnostics on stderr;
		// re-run with --debug to get verbose ssh output (-vvv).
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("ssh failed: %w", err)
	}
	return nil
}

// buildJobAPIClient constructs the API client using env values, mirroring other job commands.
func buildJobAPIClient(ctx context.Context) (*client.Client, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	envValues, err := utils.GetEnvironmentValues(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get environment values: %w", err)
	}

	accountName := envValues[utils.EnvAzureAccountName]
	projectName := envValues[utils.EnvAzureProjectName]
	tenantID := envValues[utils.EnvAzureTenantID]

	if accountName == "" || projectName == "" {
		return nil, fmt.Errorf("environment not configured. Run 'azd ai training init' first")
	}

	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantID,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	endpoint := buildProjectEndpoint(accountName, projectName)
	apiClient, err := client.NewClient(endpoint, credential)
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}
	return apiClient, nil
}
