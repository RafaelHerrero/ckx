package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/eimarfandino/ckx/awsutils"
)

// getProfiles can be overridden in tests.
var getProfiles = awsutils.GetAwsProfiles

// Default cluster provider - can be easily swapped to ManualClusterProvider in the future
var clusterProvider ClusterProvider = &AWSClusterProvider{}

// credentialsValidator can be mocked in tests
var credentialsValidator = isCredentialsValid

// awsProvider implements Provider for AWS EKS using AWS profiles and SSO.
type awsProvider struct{}

func (p *awsProvider) Name() string { return "aws" }

func (p *awsProvider) ListProfiles(ctx context.Context) ([]string, error) {
	profiles, err := getProfiles()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return profiles, nil
}

func (p *awsProvider) HasProfile(ctx context.Context, profile string) (bool, error) {
	profiles, err := p.ListProfiles(ctx)
	if err != nil {
		return false, err
	}
	for _, name := range profiles {
		if name == profile {
			return true, nil
		}
	}
	return false, nil
}

func (p *awsProvider) Prepare(ctx context.Context, profile string) (string, error) {
	if err := ensureSSO(profile); err != nil {
		return "", fmt.Errorf("failed to ensure SSO login: %w", err)
	}

	region, err := clusterProvider.GetRegion(ctx, profile)
	if err != nil {
		return "", fmt.Errorf("failed to get region for profile %s: %w", profile, err)
	}
	if region == "" {
		return "", fmt.Errorf("no region configured for profile %s", profile)
	}
	return "region " + region, nil
}

func (p *awsProvider) ListClusters(ctx context.Context, profile string) ([]ClusterRef, error) {
	names, err := clusterProvider.ListClusters(ctx, profile)
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}
	refs := make([]ClusterRef, len(names))
	for i, n := range names {
		refs[i] = ClusterRef{Name: n, Profile: profile}
	}
	return refs, nil
}

func (p *awsProvider) UseCluster(ctx context.Context, ref ClusterRef) error {
	info, err := clusterProvider.GetClusterInfo(ctx, ref.Profile, ref.Name)
	if err != nil {
		return fmt.Errorf("failed to get cluster info: %w", err)
	}
	return createOrUpdateKubeContext(ref.Profile, info)
}

func (p *awsProvider) ExportEnv(ref ClusterRef) string {
	return fmt.Sprintf("export AWS_PROFILE=%s", ref.Profile)
}

// ensureSSO attempts to validate credentials and automatically runs sso login if they're invalid
func ensureSSO(profile string) error {
	fmt.Fprintf(outputWriter, "Checking credentials for profile %s...\n", profile)

	ctx := context.Background()

	if credentialsValidator(ctx, profile) {
		fmt.Fprintln(outputWriter, "Credentials are valid")
		return nil
	}

	fmt.Fprintf(outputWriter, "Credentials for profile '%s' are expired or invalid. Attempting SSO login...\n", profile)

	cmd := execCommand("aws", "sso", "login", "--profile", profile)
	cmd.Stdin = os.Stdin
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SSO login failed for profile %s: %w", profile, err)
	}

	// Validate again after login
	if !credentialsValidator(ctx, profile) {
		return fmt.Errorf("credentials still invalid after SSO login for profile %s", profile)
	}

	fmt.Fprintln(outputWriter, "SSO login successful")
	return nil
}

// isCredentialsValid checks if the current credentials are valid using AWS SDK
func isCredentialsValid(ctx context.Context, profile string) bool {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithSharedConfigProfile(profile))
	if err != nil {
		return false
	}

	stsClient := sts.NewFromConfig(cfg)
	_, err = stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	return err == nil
}
