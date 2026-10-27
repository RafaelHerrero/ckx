package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

const twoSubscriptionsJSON = `[
 {"id":"sub-1","name":"Prod Sub","state":"Enabled","tenantId":"t1"},
 {"id":"sub-2","name":"Old Sub","state":"Disabled","tenantId":"t1"},
 {"id":"sub-3","name":"Dev Sub","state":"Enabled","tenantId":"t2"}
]`

const aksListJSON = `[
 {"name":"zeta","resourceGroup":"rg-z","location":"westeurope"},
 {"name":"alpha","resourceGroup":"rg-a","location":"northeurope"}
]`

// azScript returns a handler emulating the az CLI. tokenOK controls whether
// get-access-token succeeds (and can be flipped by "az login").
func azScript(accounts string, tokenOK *bool) func(string) (string, int) {
	return func(call string) (string, int) {
		switch {
		case strings.HasPrefix(call, "az account list"):
			return accounts, 0
		case strings.HasPrefix(call, "az account get-access-token"):
			if *tokenOK {
				return "", 0
			}
			return "", 1
		case strings.HasPrefix(call, "az login"):
			*tokenOK = true
			return "", 0
		case strings.HasPrefix(call, "az aks list --subscription sub-1"):
			return aksListJSON, 0
		case strings.HasPrefix(call, "az aks list --subscription sub-3"):
			return `[{"name":"dev-aks","resourceGroup":"rg-d","location":"westeurope"}]`, 0
		}
		return "", 0
	}
}

func newAzureTest(t *testing.T, accounts string, tokenOK bool) (*azureProvider, *scriptedExec) {
	t.Helper()
	setupTest(t)
	ok := tokenOK
	s := &scriptedExec{handler: azScript(accounts, &ok)}
	execCommand = s.command
	lookPath = func(string) (string, error) { return "/usr/bin/x", nil }
	return &azureProvider{}, s
}

func TestAzureProvider_ListProfilesSkipsDisabled(t *testing.T) {
	p, _ := newAzureTest(t, twoSubscriptionsJSON, true)

	got, err := p.ListProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "Prod Sub,Dev Sub" {
		t.Errorf("unexpected profiles: %v", got)
	}
}

func TestAzureProvider_NotInstalledIsSilent(t *testing.T) {
	p, s := newAzureTest(t, twoSubscriptionsJSON, true)
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }

	names, err := p.ListProfiles(context.Background())
	if err != nil || len(names) != 0 {
		t.Fatalf("expected no profiles/no error, got %v, %v", names, err)
	}
	ok, err := p.HasProfile(context.Background(), "Prod Sub")
	if err != nil || ok {
		t.Fatalf("expected HasProfile=false, got %v, %v", ok, err)
	}
	if len(s.calls) != 0 {
		t.Errorf("az must not be invoked when it is not installed, calls: %v", s.calls)
	}
}

func TestAzureProvider_HasProfileByNameOrID(t *testing.T) {
	p, _ := newAzureTest(t, twoSubscriptionsJSON, true)
	ctx := context.Background()

	for profile, want := range map[string]bool{
		"Prod Sub": true,
		"prod sub": true,
		"sub-3":    true,
		"Old Sub":  false, // disabled
		"missing":  false,
	} {
		got, err := p.HasProfile(ctx, profile)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("HasProfile(%q) = %v, want %v", profile, got, want)
		}
	}
}

func TestAzureProvider_PrepareValidCredentialsSkipsLogin(t *testing.T) {
	p, s := newAzureTest(t, twoSubscriptionsJSON, true)

	scope, err := p.Prepare(context.Background(), "Prod Sub")
	if err != nil {
		t.Fatal(err)
	}
	if scope != "subscription Prod Sub" {
		t.Errorf("unexpected scope %q", scope)
	}
	if s.called("az login") {
		t.Errorf("az login must not run with valid credentials: %v", s.calls)
	}
}

func TestAzureProvider_PrepareLogsInWhenTokenExpired(t *testing.T) {
	p, s := newAzureTest(t, twoSubscriptionsJSON, false)

	if _, err := p.Prepare(context.Background(), "Dev Sub"); err != nil {
		t.Fatal(err)
	}
	if !s.called("az login --output none --tenant t2") {
		t.Errorf("expected az login scoped to the subscription tenant, calls: %v", s.calls)
	}
}

func TestAzureProvider_PrepareFailsWhenLoginDoesNotHelp(t *testing.T) {
	p, s := newAzureTest(t, twoSubscriptionsJSON, false)
	base := s.handler
	s.handler = func(call string) (string, int) {
		if strings.HasPrefix(call, "az login") {
			return "", 0 // login "succeeds" but the token stays invalid
		}
		return base(call)
	}

	_, err := p.Prepare(context.Background(), "Prod Sub")
	if err == nil || !strings.Contains(err.Error(), "still invalid") {
		t.Fatalf("expected 'still invalid' error, got %v", err)
	}
}

func TestAzureProvider_PrepareUnknownSubscription(t *testing.T) {
	p, _ := newAzureTest(t, twoSubscriptionsJSON, true)

	_, err := p.Prepare(context.Background(), "Nope")
	if err == nil || !strings.Contains(err.Error(), "not found after az login") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestAzureProvider_ListClustersSorted(t *testing.T) {
	p, _ := newAzureTest(t, twoSubscriptionsJSON, true)

	refs, err := p.ListClusters(context.Background(), "Prod Sub")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Name != "alpha" || refs[1].Name != "zeta" {
		t.Fatalf("expected sorted [alpha zeta], got %+v", refs)
	}
	if refs[0].Profile != "sub-1" || refs[0].Group != "rg-a" || refs[0].Detail != "rg-a, northeurope" {
		t.Errorf("unexpected ref: %+v", refs[0])
	}
}

func TestAzureProvider_ListAllClusters(t *testing.T) {
	p, _ := newAzureTest(t, twoSubscriptionsJSON, true)

	refs, err := p.ListAllClusters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range refs {
		names = append(names, r.Name+"@"+r.Profile)
	}
	// Disabled sub-2 is skipped; results are sorted by name.
	want := "alpha@sub-1,dev-aks@sub-3,zeta@sub-1"
	if strings.Join(names, ",") != want {
		t.Errorf("got %v, want %s", names, want)
	}
	if !strings.Contains(refs[1].Detail, "Dev Sub") {
		t.Errorf("expected subscription name in detail, got %q", refs[1].Detail)
	}
}

func TestAzureProvider_ListAllClustersLogsInWithoutSession(t *testing.T) {
	setupTest(t)
	loggedIn := false
	s := &scriptedExec{handler: func(call string) (string, int) {
		switch {
		case strings.HasPrefix(call, "az login"):
			loggedIn = true
			return "", 0
		case strings.HasPrefix(call, "az account list"):
			if loggedIn {
				return twoSubscriptionsJSON, 0
			}
			return "[]", 0
		case strings.HasPrefix(call, "az aks list"):
			return "[]", 0
		}
		return "", 0
	}}
	execCommand = s.command
	lookPath = func(string) (string, error) { return "/usr/bin/az", nil }

	if _, err := (&azureProvider{}).ListAllClusters(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.called("az login") {
		t.Errorf("expected az login without an active session, calls: %v", s.calls)
	}
}

func TestAzureProvider_UseClusterRunsCredentialsAndKubelogin(t *testing.T) {
	p, s := newAzureTest(t, twoSubscriptionsJSON, true)

	ref := ClusterRef{Name: "alpha", Profile: "sub-1", Group: "rg-a"}
	if err := p.UseCluster(context.Background(), ref); err != nil {
		t.Fatal(err)
	}

	wantCalls := []string{
		"az aks get-credentials --resource-group rg-a --name alpha --subscription sub-1 --overwrite-existing",
		"kubelogin convert-kubeconfig --login azurecli --context alpha",
	}
	for _, want := range wantCalls {
		found := false
		for _, c := range s.calls {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing call %q, calls: %v", want, s.calls)
		}
	}
}

func TestAzureProvider_UseClusterWithoutKubeloginWarns(t *testing.T) {
	p, s := newAzureTest(t, twoSubscriptionsJSON, true)
	lookPath = func(name string) (string, error) {
		if name == "kubelogin" {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + name, nil
	}

	buf := setupBufferAfter(t)
	if err := p.UseCluster(context.Background(), ClusterRef{Name: "alpha", Profile: "sub-1", Group: "rg-a"}); err != nil {
		t.Fatal(err)
	}
	if s.called("kubelogin") {
		t.Error("kubelogin must not be executed when missing")
	}
	if !strings.Contains(buf.String(), "kubelogin not found") {
		t.Errorf("expected warning, got %q", buf.String())
	}
}

func TestAzureProvider_UseClusterGetCredentialsFailure(t *testing.T) {
	p, s := newAzureTest(t, twoSubscriptionsJSON, true)
	base := s.handler
	s.handler = func(call string) (string, int) {
		if strings.HasPrefix(call, "az aks get-credentials") {
			return "", 1
		}
		return base(call)
	}

	err := p.UseCluster(context.Background(), ClusterRef{Name: "alpha", Profile: "sub-1", Group: "rg-a"})
	if err == nil || !strings.Contains(err.Error(), "get-credentials") {
		t.Fatalf("expected get-credentials error, got %v", err)
	}
	if s.called("kubelogin") {
		t.Error("kubelogin must not run after a failed get-credentials")
	}
}

func TestAzureProvider_ExportEnv(t *testing.T) {
	got := (&azureProvider{}).ExportEnv(ClusterRef{Profile: "sub-1"})
	if got != "export AZURE_SUBSCRIPTION_ID=sub-1" {
		t.Errorf("unexpected export line %q", got)
	}
}

// setupBufferAfter re-points outputWriter at a fresh buffer (setupTest has
// already registered the restore).
func setupBufferAfter(t *testing.T) *strings.Builder {
	t.Helper()
	var sb strings.Builder
	outputWriter = &sb
	return &sb
}

// --- extra CA bundle ---

func writeKubeconfig(t *testing.T, ca string) {
	t.Helper()
	cfg := api.NewConfig()
	cfg.Clusters["aks-cluster"] = &api.Cluster{Server: "https://example", CertificateAuthorityData: []byte(ca)}
	cfg.AuthInfos["user"] = api.NewAuthInfo()
	cfg.Contexts["alpha"] = &api.Context{Cluster: "aks-cluster", AuthInfo: "user"}
	cfg.CurrentContext = "alpha"

	path := getDefaultKubeConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatal(err)
	}
}

func readCA(t *testing.T) string {
	t.Helper()
	cfg, err := clientcmd.LoadFromFile(getDefaultKubeConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	return string(cfg.Clusters["aks-cluster"].CertificateAuthorityData)
}

func TestAppendExtraCA(t *testing.T) {
	setupTest(t)
	writeKubeconfig(t, "CLUSTER-CA\n")

	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(bundle, []byte("CORP-CA\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := appendExtraCA("alpha", bundle); err != nil {
		t.Fatal(err)
	}
	if got := readCA(t); got != "CLUSTER-CA\nCORP-CA\n" {
		t.Errorf("unexpected merged CA: %q", got)
	}

	// Running it again must not duplicate the bundle.
	if err := appendExtraCA("alpha", bundle); err != nil {
		t.Fatal(err)
	}
	if got := readCA(t); got != "CLUSTER-CA\nCORP-CA\n" {
		t.Errorf("CA bundle was appended twice: %q", got)
	}
}

func TestAppendExtraCA_NoBundleIsNoop(t *testing.T) {
	setupTest(t)
	writeKubeconfig(t, "CLUSTER-CA\n")

	for _, path := range []string{"", filepath.Join(t.TempDir(), "does-not-exist.pem")} {
		if err := appendExtraCA("alpha", path); err != nil {
			t.Fatalf("path %q: unexpected error %v", path, err)
		}
	}
	if got := readCA(t); got != "CLUSTER-CA\n" {
		t.Errorf("CA must be untouched, got %q", got)
	}
}

func TestAppendExtraCA_UnknownContext(t *testing.T) {
	setupTest(t)
	writeKubeconfig(t, "CLUSTER-CA\n")
	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	_ = os.WriteFile(bundle, []byte("CORP-CA\n"), 0o600)

	err := appendExtraCA("missing", bundle)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected context-not-found error, got %v", err)
	}
}
