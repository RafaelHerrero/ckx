package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// ClusterRef identifies a cluster that can be selected by the user.
type ClusterRef struct {
	Name string
	// Profile is the provider-specific scope the cluster lives in
	// (AWS profile name, Azure subscription ID).
	Profile string
	// Group is an optional grouping (Azure resource group).
	Group string
	// Detail is extra text shown next to the name when selecting.
	Detail string
}

// Display returns the text shown in the cluster selection list.
func (c ClusterRef) Display() string {
	if c.Detail == "" {
		return c.Name
	}
	return fmt.Sprintf("%s (%s)", c.Name, c.Detail)
}

// Provider abstracts a cloud (AWS, Azure, ...) that hosts Kubernetes clusters.
type Provider interface {
	// Name is the identifier used by --provider and in listings.
	Name() string
	// ListProfiles returns the profiles/subscriptions known locally.
	// It returns no profiles (and no error) if the provider isn't set up.
	ListProfiles(ctx context.Context) ([]string, error)
	// HasProfile reports whether the given profile/subscription belongs to this provider.
	HasProfile(ctx context.Context, profile string) (bool, error)
	// Prepare makes sure the user is logged in for the profile and returns a
	// short description of the scope (e.g. "region eu-west-1").
	Prepare(ctx context.Context, profile string) (string, error)
	// ListClusters returns the clusters available for the profile.
	ListClusters(ctx context.Context, profile string) ([]ClusterRef, error)
	// UseCluster updates the kubeconfig so that it points at the cluster.
	UseCluster(ctx context.Context, ref ClusterRef) error
	// ExportEnv returns a shell line to eval for --export, or "".
	ExportEnv(ref ClusterRef) string
}

// allClustersLister is implemented by providers that can list the clusters
// of every profile/subscription in one go.
type allClustersLister interface {
	ListAllClusters(ctx context.Context) ([]ClusterRef, error)
}

// providers is the registry of known providers. Overridden in tests.
var providers = []Provider{&awsProvider{}, &azureProvider{}}

type profileEntry struct {
	provider Provider
	name     string
}

// collectProfiles gathers the profiles of every provider. Provider failures
// are returned separately so that one broken provider doesn't hide the others.
func collectProfiles(ctx context.Context) ([]profileEntry, []error) {
	var entries []profileEntry
	var errs []error
	for _, p := range providers {
		names, err := p.ListProfiles(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
			continue
		}
		for _, n := range names {
			entries = append(entries, profileEntry{provider: p, name: n})
		}
	}
	return entries, errs
}

func providerNames() string {
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.Name())
	}
	return strings.Join(names, ", ")
}

func providerByName(name string) (Provider, error) {
	for _, p := range providers {
		if strings.EqualFold(p.Name(), name) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("unknown provider %q (available: %s)", name, providerNames())
}

// resolveProvider picks the provider for a profile. If forced is set it wins,
// otherwise the provider that knows the profile is auto-detected.
func resolveProvider(ctx context.Context, profile, forced string, warn io.Writer) (Provider, error) {
	if forced != "" {
		return providerByName(forced)
	}

	var matches []Provider
	for _, p := range providers {
		ok, err := p.HasProfile(ctx, profile)
		if err != nil {
			fmt.Fprintf(warn, "Warning: %s: %v\n", p.Name(), err)
			continue
		}
		if ok {
			matches = append(matches, p)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("profile %q not found (providers checked: %s). "+
			"If it is an Azure subscription and you are not logged in yet, run 'az login' or pass --provider azure",
			profile, providerNames())
	case 1:
		return matches[0], nil
	default:
		names := make([]string, len(matches))
		for i, p := range matches {
			names[i] = p.Name()
		}
		return nil, fmt.Errorf("profile %q exists in multiple providers (%s); pass --provider to choose",
			profile, strings.Join(names, ", "))
	}
}
