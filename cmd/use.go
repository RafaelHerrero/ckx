package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

var execCommand = exec.Command
var outputWriter io.Writer = os.Stdout

var (
	exportFlag   bool
	providerFlag string
	allFlag      bool
)

// candidate is a selectable cluster together with the provider that owns it.
type candidate struct {
	provider Provider
	ref      ClusterRef
}

var useCmd = &cobra.Command{
	Use:   "use [profile]",
	Short: "Use an AWS profile or Azure subscription and set kubeconfig for a cluster",
	Long: `Use an AWS profile or Azure subscription and set kubeconfig for a cluster.

The provider is auto-detected from the profile name (AWS profiles in
~/.aws/config, Azure subscriptions known to the az CLI). Use --provider to
force one when the name exists in both.

With --all, no profile is needed: the clusters of every Azure subscription are
listed together so you can pick one.`,
	Args: func(cmd *cobra.Command, args []string) error {
		if allFlag {
			return cobra.MaximumNArgs(0)(cmd, args)
		}
		return cobra.ExactArgs(1)(cmd, args)
	},
	Run: func(cmd *cobra.Command, args []string) {
		profile := ""
		if len(args) > 0 {
			profile = args[0]
		}
		runUse(context.Background(), profile, providerFlag, allFlag)
	},
}

// runUse logs in to the given profile (or every subscription when all is set),
// lets the user pick a cluster and updates the kubeconfig.
func runUse(ctx context.Context, profile, forcedProvider string, all bool) {
	// If export flag is set, redirect informational output to stderr
	originalWriter := outputWriter
	if exportFlag {
		outputWriter = os.Stderr
	}
	defer func() { outputWriter = originalWriter }()

	var (
		candidates []candidate
		scope      string
	)

	if all {
		var err error
		candidates, err = gatherAllClusters(ctx)
		if err != nil {
			fmt.Fprintln(outputWriter, "Failed to list clusters:", err)
			return
		}
		scope = "all subscriptions"
	} else {
		provider, err := resolveProvider(ctx, profile, forcedProvider, outputWriter)
		if err != nil {
			fmt.Fprintln(outputWriter, err)
			return
		}

		scope, err = provider.Prepare(ctx, profile)
		if err != nil {
			fmt.Fprintln(outputWriter, "Error:", err)
			return
		}

		refs, err := provider.ListClusters(ctx, profile)
		if err != nil {
			fmt.Fprintln(outputWriter, "Failed to list clusters:", err)
			return
		}
		for _, r := range refs {
			candidates = append(candidates, candidate{provider: provider, ref: r})
		}
	}

	if len(candidates) == 0 {
		fmt.Fprintln(outputWriter, "No clusters found in this account")
		return
	}

	selected := candidates[0]
	if len(candidates) == 1 {
		fmt.Fprintln(outputWriter, "Only one cluster found:", selected.ref.Name)
	} else {
		fmt.Fprintln(outputWriter, "Available clusters in", scope)
		for i, c := range candidates {
			label := c.ref.Display()
			if all {
				label = fmt.Sprintf("%s [%s]", label, c.provider.Name())
			}
			fmt.Fprintf(outputWriter, "[%d] %s\n", i+1, label)
		}

		fmt.Fprint(outputWriter, "Select cluster by number: ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintln(outputWriter, "Error reading input:", err)
			return
		}
		choice, err := strconv.Atoi(strings.TrimSpace(input))
		if err != nil || choice < 1 || choice > len(candidates) {
			fmt.Fprintln(outputWriter, "Invalid selection")
			return
		}
		selected = candidates[choice-1]
	}

	updateKubeconfig(ctx, selected, exportFlag)
}

func init() {
	rootCmd.AddCommand(useCmd)
	useCmd.Flags().BoolVar(&exportFlag, "export", false, "Output shell commands for eval (export AWS_PROFILE / AZURE_SUBSCRIPTION_ID)")
	useCmd.Flags().StringVar(&providerFlag, "provider", "", "Force the provider (aws, azure) instead of auto-detecting it")
	useCmd.Flags().BoolVar(&allFlag, "all", false, "List clusters of all subscriptions (Azure) and pick one")
}

// gatherAllClusters collects the clusters of every provider that supports
// listing across all of its subscriptions, honouring --provider.
func gatherAllClusters(ctx context.Context) ([]candidate, error) {
	selected := providers
	if providerFlag != "" {
		p, err := providerByName(providerFlag)
		if err != nil {
			return nil, err
		}
		selected = []Provider{p}
	}

	var out []candidate
	var supported bool
	for _, p := range selected {
		lister, ok := p.(allClustersLister)
		if !ok {
			continue
		}
		supported = true
		refs, err := lister.ListAllClusters(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name(), err)
		}
		for _, r := range refs {
			out = append(out, candidate{provider: p, ref: r})
		}
	}
	if !supported {
		return nil, fmt.Errorf("--all is not supported by the selected provider (supported: azure)")
	}
	return out, nil
}

func updateKubeconfig(ctx context.Context, c candidate, export bool) {
	fmt.Fprintln(outputWriter, "Updating kubeconfig for cluster:", c.ref.Name)

	if err := c.provider.UseCluster(ctx, c.ref); err != nil {
		fmt.Fprintf(outputWriter, "Failed to update kubeconfig: %v\n", err)
		return
	}

	fmt.Fprintf(outputWriter, "Successfully updated kubeconfig for cluster: %s\n", c.ref.Name)
	fmt.Fprintf(outputWriter, "Current context set to: %s\n", c.ref.Name)

	// If export flag is set, output shell commands
	if export {
		if line := c.provider.ExportEnv(c.ref); line != "" {
			fmt.Fprintln(os.Stdout, line)
		}
	}
}

func getDefaultKubeConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".kube", "config")
}

func createOrUpdateKubeContext(profile string, clusterInfo *ClusterInfo) error {
	configPath := getDefaultKubeConfigPath()
	if configPath == "" {
		return fmt.Errorf("could not determine kubeconfig path")
	}

	// Ensure .kube directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create .kube directory: %w", err)
	}

	loadingRules := clientcmd.ClientConfigLoadingRules{
		Precedence: []string{configPath},
	}
	config, err := loadingRules.Load()
	if err != nil {
		// If config doesn't exist, create a new one
		config = api.NewConfig()
	}

	// Configure cluster
	clusterConfig := config.Clusters[clusterInfo.Arn]
	if clusterConfig == nil {
		clusterConfig = api.NewCluster()
	}
	clusterConfig.Server = clusterInfo.Endpoint
	clusterConfig.CertificateAuthorityData = clusterInfo.CertificateData
	clusterConfig.LocationOfOrigin = configPath

	// Configure auth info
	authInfo := config.AuthInfos[clusterInfo.Arn]
	if authInfo == nil {
		authInfo = api.NewAuthInfo()
	}
	authInfo.LocationOfOrigin = configPath
	authInfo.Exec = &api.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1beta1",
		Command:    clusterInfo.AuthCommand,
		Args:       clusterInfo.AuthArgs,
	}

	// Add environment variables if present
	if len(clusterInfo.AuthEnv) > 0 {
		var envVars []api.ExecEnvVar
		for key, value := range clusterInfo.AuthEnv {
			envVars = append(envVars, api.ExecEnvVar{
				Name:  key,
				Value: value,
			})
		}
		authInfo.Exec.Env = envVars
	}

	// Configure context
	context := config.Contexts[clusterInfo.Name]
	if context == nil {
		context = api.NewContext()
	}
	context.LocationOfOrigin = configPath
	context.Cluster = clusterInfo.Arn
	context.AuthInfo = clusterInfo.Arn

	// Update config
	config.Clusters[clusterInfo.Arn] = clusterConfig
	config.AuthInfos[clusterInfo.Arn] = authInfo
	config.Contexts[clusterInfo.Name] = context
	config.CurrentContext = clusterInfo.Name

	// Write config
	configAccess := clientcmd.NewDefaultPathOptions()
	configAccess.GlobalFile = configPath
	if err := clientcmd.ModifyConfig(configAccess, *config, true); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	return nil
}
