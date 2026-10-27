package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// setupTest isolates global state for a test: it swaps in the given providers,
// captures output, points HOME at a temp dir (so the real kubeconfig is never
// touched) and resets command flags. Everything is restored on cleanup.
func setupTest(t *testing.T, ps ...Provider) *bytes.Buffer {
	t.Helper()

	origProviders := providers
	origWriter := outputWriter
	origExec := execCommand
	origLook := lookPath
	origCA := extraCABundlePath

	t.Setenv("HOME", t.TempDir())
	t.Setenv("KUBECONFIG", "")

	var buf bytes.Buffer
	providers = ps
	outputWriter = &buf
	extraCABundlePath = func() string { return "" }
	exportFlag, providerFlag, allFlag = false, "", false

	t.Cleanup(func() {
		providers = origProviders
		outputWriter = origWriter
		execCommand = origExec
		lookPath = origLook
		extraCABundlePath = origCA
		exportFlag, providerFlag, allFlag = false, "", false
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	return &buf
}

// withStdin replaces os.Stdin with a pipe that yields the given input.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	w.Close()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig; r.Close() })
}

// runRoot executes the root command with the given args.
func runRoot(t *testing.T, args ...string) error {
	t.Helper()
	rootCmd.SetArgs(args)
	return rootCmd.Execute()
}

// scriptedExec replaces execCommand with processes that print a canned
// output and exit with a canned code, recording every call.
type scriptedExec struct {
	calls   []string
	handler func(call string) (stdout string, exitCode int)
}

func (s *scriptedExec) command(name string, args ...string) *exec.Cmd {
	call := strings.TrimSpace(name + " " + strings.Join(args, " "))
	s.calls = append(s.calls, call)
	out, code := s.handler(call)
	return exec.Command("sh", "-c", `printf '%s' "$1"; exit "$2"`, "sh", out, strconv.Itoa(code))
}

func (s *scriptedExec) called(prefix string) bool {
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// fakeProvider is a configurable Provider for command-level tests.
type fakeProvider struct {
	name       string
	profiles   []string
	clusters   []ClusterRef
	prepareErr error
	export     string

	prepared []string
	used     []ClusterRef
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) ListProfiles(ctx context.Context) ([]string, error) {
	return f.profiles, nil
}

func (f *fakeProvider) HasProfile(ctx context.Context, profile string) (bool, error) {
	for _, p := range f.profiles {
		if p == profile {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeProvider) Prepare(ctx context.Context, profile string) (string, error) {
	f.prepared = append(f.prepared, profile)
	if f.prepareErr != nil {
		return "", f.prepareErr
	}
	return "scope of " + profile, nil
}

func (f *fakeProvider) ListClusters(ctx context.Context, profile string) ([]ClusterRef, error) {
	return f.clusters, nil
}

func (f *fakeProvider) UseCluster(ctx context.Context, ref ClusterRef) error {
	f.used = append(f.used, ref)
	return nil
}

func (f *fakeProvider) ExportEnv(ref ClusterRef) string {
	if f.export == "" {
		return ""
	}
	return fmt.Sprintf("%s=%s", f.export, ref.Profile)
}

// fakeAllProvider additionally supports listing across all profiles.
type fakeAllProvider struct {
	*fakeProvider
	all []ClusterRef
}

func (f *fakeAllProvider) ListAllClusters(ctx context.Context) ([]ClusterRef, error) {
	return f.all, nil
}
