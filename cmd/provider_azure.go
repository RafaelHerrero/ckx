package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// lookPath can be overridden in tests.
var lookPath = exec.LookPath

type azureSubscription struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	TenantID string `json:"tenantId"`
}

// azureProvider implements Provider for Azure AKS. It shells out to the
// `az` CLI for login and discovery, and to `kubelogin` for cluster auth.
type azureProvider struct {
	subs   []azureSubscription
	loaded bool
}

func (p *azureProvider) Name() string { return "azure" }

func (p *azureProvider) available() bool {
	_, err := lookPath("az")
	return err == nil
}

// az runs the az CLI and returns stdout.
func (p *azureProvider) az(args ...string) ([]byte, error) {
	cmd := execCommand("az", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("az %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return nil, fmt.Errorf("az %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// subscriptions returns the enabled subscriptions of the current az session.
func (p *azureProvider) subscriptions() ([]azureSubscription, error) {
	if p.loaded {
		return p.subs, nil
	}

	out, err := p.az("account", "list", "--output", "json")
	if err != nil {
		return nil, err
	}

	var all []azureSubscription
	if trimmed := bytes.TrimSpace(out); len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &all); err != nil {
			return nil, fmt.Errorf("failed to parse az account list output: %w", err)
		}
	}

	subs := make([]azureSubscription, 0, len(all))
	for _, s := range all {
		if s.State == "" || strings.EqualFold(s.State, "Enabled") {
			subs = append(subs, s)
		}
	}
	p.subs = subs
	p.loaded = true
	return subs, nil
}

// findSubscription matches by subscription name or ID (case-insensitive).
func (p *azureProvider) findSubscription(profile string) (azureSubscription, bool, error) {
	subs, err := p.subscriptions()
	if err != nil {
		return azureSubscription{}, false, err
	}
	for _, s := range subs {
		if strings.EqualFold(s.Name, profile) || strings.EqualFold(s.ID, profile) {
			return s, true, nil
		}
	}
	return azureSubscription{}, false, nil
}

func (p *azureProvider) ListProfiles(ctx context.Context) ([]string, error) {
	if !p.available() {
		return nil, nil
	}
	subs, err := p.subscriptions()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(subs))
	for i, s := range subs {
		names[i] = s.Name
	}
	return names, nil
}

func (p *azureProvider) HasProfile(ctx context.Context, profile string) (bool, error) {
	if !p.available() {
		return false, nil
	}
	_, found, err := p.findSubscription(profile)
	return found, err
}

func (p *azureProvider) login(tenant string) error {
	args := []string{"login", "--output", "none"}
	if tenant != "" {
		args = append(args, "--tenant", tenant)
	}
	cmd := execCommand("az", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("az login failed: %w", err)
	}
	p.loaded = false
	return nil
}

func (p *azureProvider) tokenValid(subscriptionID string) bool {
	_, err := p.az("account", "get-access-token", "--subscription", subscriptionID, "--output", "none")
	return err == nil
}

func (p *azureProvider) Prepare(ctx context.Context, profile string) (string, error) {
	if !p.available() {
		return "", fmt.Errorf("az CLI not found in PATH")
	}

	fmt.Fprintf(outputWriter, "Checking credentials for subscription %s...\n", profile)

	sub, found, err := p.findSubscription(profile)
	if err != nil || !found {
		fmt.Fprintln(outputWriter, "Subscription not found in the current az session. Attempting az login...")
		if err := p.login(""); err != nil {
			return "", err
		}
		sub, found, err = p.findSubscription(profile)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fmt.Errorf("subscription %q not found after az login", profile)
		}
	}

	if p.tokenValid(sub.ID) {
		fmt.Fprintln(outputWriter, "Credentials are valid")
		return "subscription " + sub.Name, nil
	}

	fmt.Fprintf(outputWriter, "Credentials for subscription '%s' are expired or invalid. Attempting az login...\n", sub.Name)
	if err := p.login(sub.TenantID); err != nil {
		return "", err
	}
	if !p.tokenValid(sub.ID) {
		return "", fmt.Errorf("credentials still invalid after az login for subscription %s", sub.Name)
	}
	fmt.Fprintln(outputWriter, "az login successful")
	return "subscription " + sub.Name, nil
}

func (p *azureProvider) clustersIn(sub azureSubscription, withSubscription bool) ([]ClusterRef, error) {
	out, err := p.az("aks", "list", "--subscription", sub.ID, "--output", "json")
	if err != nil {
		return nil, err
	}

	var raw []struct {
		Name          string `json:"name"`
		ResourceGroup string `json:"resourceGroup"`
		Location      string `json:"location"`
	}
	if trimmed := bytes.TrimSpace(out); len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return nil, fmt.Errorf("failed to parse az aks list output: %w", err)
		}
	}

	refs := make([]ClusterRef, 0, len(raw))
	for _, c := range raw {
		detail := c.ResourceGroup
		if c.Location != "" {
			detail += ", " + c.Location
		}
		if withSubscription {
			detail += ", " + sub.Name
		}
		refs = append(refs, ClusterRef{
			Name:    c.Name,
			Profile: sub.ID,
			Group:   c.ResourceGroup,
			Detail:  detail,
		})
	}
	sortRefs(refs)
	return refs, nil
}

func sortRefs(refs []ClusterRef) {
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Detail < refs[j].Detail
	})
}

func (p *azureProvider) ListClusters(ctx context.Context, profile string) ([]ClusterRef, error) {
	sub, found, err := p.findSubscription(profile)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("subscription %q not found", profile)
	}
	return p.clustersIn(sub, false)
}

// ListAllClusters lists the AKS clusters of every enabled subscription.
func (p *azureProvider) ListAllClusters(ctx context.Context) ([]ClusterRef, error) {
	if !p.available() {
		return nil, fmt.Errorf("az CLI not found in PATH")
	}

	subs, err := p.subscriptions()
	if err != nil || len(subs) == 0 {
		fmt.Fprintln(outputWriter, "No active az session found. Attempting az login...")
		if err := p.login(""); err != nil {
			return nil, err
		}
		if subs, err = p.subscriptions(); err != nil {
			return nil, err
		}
	}

	var all []ClusterRef
	for _, sub := range subs {
		refs, err := p.clustersIn(sub, true)
		if err != nil {
			fmt.Fprintf(outputWriter, "Warning: failed to list clusters in subscription %s: %v\n", sub.Name, err)
			continue
		}
		all = append(all, refs...)
	}
	sortRefs(all)
	return all, nil
}

func (p *azureProvider) UseCluster(ctx context.Context, ref ClusterRef) error {
	cmd := execCommand("az", "aks", "get-credentials",
		"--resource-group", ref.Group,
		"--name", ref.Name,
		"--subscription", ref.Profile,
		"--overwrite-existing")
	cmd.Stdin = os.Stdin
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("az aks get-credentials failed: %w", err)
	}

	if _, err := lookPath("kubelogin"); err != nil {
		fmt.Fprintln(outputWriter, "Warning: kubelogin not found in PATH, skipping kubeconfig conversion (Entra ID clusters will not authenticate)")
	} else {
		convert := execCommand("kubelogin", "convert-kubeconfig", "--login", "azurecli", "--context", ref.Name)
		convert.Stdout = outputWriter
		convert.Stderr = outputWriter
		if err := convert.Run(); err != nil {
			return fmt.Errorf("kubelogin convert-kubeconfig failed: %w", err)
		}
	}

	if err := appendExtraCA(ref.Name, extraCABundlePath()); err != nil {
		fmt.Fprintf(outputWriter, "Warning: failed to append extra CA bundle: %v\n", err)
	}
	return nil
}

func (p *azureProvider) ExportEnv(ref ClusterRef) string {
	return fmt.Sprintf("export AZURE_SUBSCRIPTION_ID=%s", ref.Profile)
}
