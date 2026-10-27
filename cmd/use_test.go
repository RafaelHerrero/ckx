package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
)

// Mock ClusterProvider for testing the AWS provider
type mockClusterProvider struct {
	shouldFailGetRegion      bool
	shouldFailListClusters   bool
	shouldFailGetClusterInfo bool
	clusters                 []string
	region                   string
}

func (m *mockClusterProvider) GetRegion(ctx context.Context, profile string) (string, error) {
	if m.shouldFailGetRegion {
		return "", fmt.Errorf("failed to get region")
	}
	return m.region, nil
}

func (m *mockClusterProvider) ListClusters(ctx context.Context, profile string) ([]string, error) {
	if m.shouldFailListClusters {
		return nil, fmt.Errorf("failed to list clusters")
	}
	return m.clusters, nil
}

func (m *mockClusterProvider) GetClusterInfo(ctx context.Context, profile, clusterName string) (*ClusterInfo, error) {
	if m.shouldFailGetClusterInfo {
		return nil, fmt.Errorf("failed to get cluster info")
	}

	return &ClusterInfo{
		Name:            clusterName,
		Endpoint:        "https://test-cluster.sk1.eu-west-1.eks.amazonaws.com",
		CertificateData: []byte("fake-certificate-data"),
		Region:          m.region,
		Arn:             "arn:aws:eks:eu-west-1:123456789012:cluster/" + clusterName,
		AuthCommand:     "aws",
		AuthArgs: []string{
			"eks", "get-token",
			"--cluster-name", clusterName,
			"--region", m.region,
		},
		AuthEnv: map[string]string{"AWS_PROFILE": profile},
	}, nil
}

// setupAWS wires the real awsProvider to mocked profile lookup, credentials
// and cluster discovery.
func setupAWS(t *testing.T, mock *mockClusterProvider, profiles ...string) {
	t.Helper()

	origCP, origCV, origGP := clusterProvider, credentialsValidator, getProfiles
	t.Cleanup(func() {
		clusterProvider, credentialsValidator, getProfiles = origCP, origCV, origGP
	})

	clusterProvider = mock
	credentialsValidator = func(ctx context.Context, profile string) bool { return true }
	getProfiles = func() ([]string, error) { return profiles, nil }
}

func TestUseCommand_MockAWS(t *testing.T) {
	buf := setupTest(t, &awsProvider{})
	setupAWS(t, &mockClusterProvider{
		region:   "eu-west-1",
		clusters: []string{"cluster-one", "cluster-two"},
	}, "mock-profile")
	withStdin(t, "1\n")

	if err := runRoot(t, "use", "mock-profile"); err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"Available clusters in region eu-west-1",
		"[1] cluster-one",
		"Updating kubeconfig for cluster: cluster-one",
		"Successfully updated kubeconfig for cluster: cluster-one",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}

	// The kubeconfig must have been written to the temp HOME with AWS exec auth.
	cfg, err := clientcmd.LoadFromFile(getDefaultKubeConfigPath())
	if err != nil {
		t.Fatalf("kubeconfig was not written: %v", err)
	}
	if cfg.CurrentContext != "cluster-one" {
		t.Errorf("expected current context cluster-one, got %q", cfg.CurrentContext)
	}
	ctxInfo := cfg.Contexts["cluster-one"]
	if ctxInfo == nil {
		t.Fatal("context cluster-one missing")
	}
	auth := cfg.AuthInfos[ctxInfo.AuthInfo]
	if auth == nil || auth.Exec == nil || auth.Exec.Command != "aws" {
		t.Fatalf("expected aws exec auth, got %+v", auth)
	}
}

func TestUseCommand_AWSExport(t *testing.T) {
	setupTest(t, &awsProvider{})
	setupAWS(t, &mockClusterProvider{region: "eu-west-1", clusters: []string{"only"}}, "mock-profile")

	// --export prints the env line on stdout; capture it.
	r, w, _ := os.Pipe()
	origStdout := os.Stdout
	os.Stdout = w
	err := runRoot(t, "use", "mock-profile", "--export")
	w.Close()
	os.Stdout = origStdout
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	if got := strings.TrimSpace(string(buf[:n])); got != "export AWS_PROFILE=mock-profile" {
		t.Errorf("unexpected export line: %q", got)
	}
}

func TestAWSProvider_PrepareErrors(t *testing.T) {
	tests := []struct {
		name string
		mock *mockClusterProvider
		want string
	}{
		{"region lookup fails", &mockClusterProvider{shouldFailGetRegion: true}, "failed to get region"},
		{"no region", &mockClusterProvider{region: ""}, "no region configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTest(t, &awsProvider{})
			setupAWS(t, tt.mock, "p")
			_, err := (&awsProvider{}).Prepare(context.Background(), "p")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestAWSProvider_MissingConfigFileIsNotAnError(t *testing.T) {
	setupTest(t, &awsProvider{})
	orig := getProfiles
	t.Cleanup(func() { getProfiles = orig })
	getProfiles = func() ([]string, error) {
		return nil, fmt.Errorf("failed to load AWS config file: %w", os.ErrNotExist)
	}

	profiles, err := (&awsProvider{}).ListProfiles(context.Background())
	if err != nil || len(profiles) != 0 {
		t.Fatalf("expected no profiles and no error, got %v, %v", profiles, err)
	}
}
