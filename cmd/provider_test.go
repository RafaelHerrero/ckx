package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestResolveProvider(t *testing.T) {
	aws := &fakeProvider{name: "aws", profiles: []string{"dev", "shared"}}
	azure := &fakeProvider{name: "azure", profiles: []string{"Prod Sub", "shared"}}
	ctx := context.Background()

	tests := []struct {
		name    string
		profile string
		forced  string
		want    string
		wantErr string
	}{
		{name: "aws only", profile: "dev", want: "aws"},
		{name: "azure only", profile: "Prod Sub", want: "azure"},
		{name: "ambiguous", profile: "shared", wantErr: "--provider"},
		{name: "ambiguous resolved by flag", profile: "shared", forced: "azure", want: "azure"},
		{name: "forced is case-insensitive", profile: "dev", forced: "AWS", want: "aws"},
		{name: "not found", profile: "nope", wantErr: "not found"},
		{name: "unknown provider", profile: "dev", forced: "gcp", wantErr: "unknown provider"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTest(t, aws, azure)
			got, err := resolveProvider(ctx, tt.profile, tt.forced, &bytes.Buffer{})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Name() != tt.want {
				t.Errorf("expected provider %s, got %s", tt.want, got.Name())
			}
		})
	}
}

func TestListCommand_AllProviders(t *testing.T) {
	setupTest(t,
		&fakeProvider{name: "aws", profiles: []string{"dev"}},
		&fakeProvider{name: "azure", profiles: []string{"Prod Sub"}},
	)
	var out bytes.Buffer
	rootCmd.SetOut(&out)

	if err := runRoot(t, "list"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Available profiles:", "dev (aws)", "Prod Sub (azure)"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got:\n%s", want, got)
		}
	}
}

func TestSearchCommand_AcrossProviders(t *testing.T) {
	buf := setupTest(t,
		&fakeProvider{name: "aws", profiles: []string{"np20-dev", "prod-aws"}},
		&fakeProvider{name: "azure", profiles: []string{"Prod Sub", "sandbox"}},
	)

	if err := runRoot(t, "search", "PROD"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"prod-aws (aws)", "Prod Sub (azure)"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"np20-dev", "sandbox"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("did not expect %q in output, got:\n%s", unwanted, got)
		}
	}
}

func TestUseCommand_AutoDetectsAzure(t *testing.T) {
	aws := &fakeProvider{name: "aws", profiles: []string{"dev"}}
	azure := &fakeProvider{
		name:     "azure",
		profiles: []string{"Prod Sub"},
		clusters: []ClusterRef{{Name: "aks-prod", Profile: "sub-1", Group: "rg"}},
	}
	buf := setupTest(t, aws, azure)

	if err := runRoot(t, "use", "Prod Sub"); err != nil {
		t.Fatal(err)
	}

	if len(aws.prepared) != 0 || len(aws.used) != 0 {
		t.Errorf("aws provider must not be used, prepared=%v used=%v", aws.prepared, aws.used)
	}
	if len(azure.used) != 1 || azure.used[0].Name != "aks-prod" {
		t.Fatalf("expected aks-prod to be used, got %+v", azure.used)
	}
	if got := buf.String(); !strings.Contains(got, "Only one cluster found: aks-prod") {
		t.Errorf("unexpected output:\n%s", got)
	}
}

func TestUseCommand_AmbiguousProfile(t *testing.T) {
	aws := &fakeProvider{name: "aws", profiles: []string{"shared"}}
	azure := &fakeProvider{name: "azure", profiles: []string{"shared"}}
	buf := setupTest(t, aws, azure)

	if err := runRoot(t, "use", "shared"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "multiple providers") {
		t.Errorf("expected ambiguity message, got:\n%s", buf.String())
	}
	if len(aws.prepared)+len(azure.prepared) != 0 {
		t.Error("no provider should be prepared when the profile is ambiguous")
	}

	buf.Reset()
	azure.clusters = []ClusterRef{{Name: "c1", Profile: "shared"}}
	if err := runRoot(t, "use", "shared", "--provider", "azure"); err != nil {
		t.Fatal(err)
	}
	if len(azure.used) != 1 {
		t.Errorf("--provider azure should select azure, output:\n%s", buf.String())
	}
}

func TestUseCommand_PrepareFailureStops(t *testing.T) {
	azure := &fakeProvider{
		name:       "azure",
		profiles:   []string{"sub"},
		prepareErr: context.DeadlineExceeded,
		clusters:   []ClusterRef{{Name: "c1"}},
	}
	buf := setupTest(t, azure)

	if err := runRoot(t, "use", "sub"); err != nil {
		t.Fatal(err)
	}
	if len(azure.used) != 0 {
		t.Error("cluster must not be used when Prepare fails")
	}
	if !strings.Contains(buf.String(), "Error:") {
		t.Errorf("expected error output, got:\n%s", buf.String())
	}
}

func TestUseCommand_SelectsFromMultipleClusters(t *testing.T) {
	azure := &fakeProvider{
		name:     "azure",
		profiles: []string{"sub"},
		clusters: []ClusterRef{
			{Name: "first", Profile: "sub-1", Detail: "rg1, westeurope"},
			{Name: "second", Profile: "sub-1", Detail: "rg2, northeurope"},
		},
	}
	buf := setupTest(t, azure)
	withStdin(t, "2\n")

	if err := runRoot(t, "use", "sub"); err != nil {
		t.Fatal(err)
	}
	if len(azure.used) != 1 || azure.used[0].Name != "second" {
		t.Fatalf("expected 'second' to be used, got %+v", azure.used)
	}
	if got := buf.String(); !strings.Contains(got, "[2] second (rg2, northeurope)") {
		t.Errorf("expected detail in cluster list, got:\n%s", got)
	}
}

func TestUseCommand_InvalidSelection(t *testing.T) {
	azure := &fakeProvider{
		name:     "azure",
		profiles: []string{"sub"},
		clusters: []ClusterRef{{Name: "a"}, {Name: "b"}},
	}
	buf := setupTest(t, azure)
	withStdin(t, "9\n")

	if err := runRoot(t, "use", "sub"); err != nil {
		t.Fatal(err)
	}
	if len(azure.used) != 0 || !strings.Contains(buf.String(), "Invalid selection") {
		t.Errorf("expected invalid selection, used=%v output:\n%s", azure.used, buf.String())
	}
}

func TestUseCommand_All(t *testing.T) {
	aws := &fakeProvider{name: "aws", profiles: []string{"dev"}}
	azure := &fakeAllProvider{
		fakeProvider: &fakeProvider{name: "azure"},
		all: []ClusterRef{
			{Name: "aks-a", Profile: "sub-1", Group: "rg1", Detail: "rg1, westeurope, Sub One"},
			{Name: "aks-b", Profile: "sub-2", Group: "rg2", Detail: "rg2, westeurope, Sub Two"},
		},
	}
	buf := setupTest(t, aws, azure)
	withStdin(t, "2\n")

	if err := runRoot(t, "use", "--all"); err != nil {
		t.Fatal(err)
	}
	if len(azure.used) != 1 || azure.used[0].Name != "aks-b" || azure.used[0].Profile != "sub-2" {
		t.Fatalf("expected aks-b in sub-2 to be used, got %+v", azure.used)
	}
	if got := buf.String(); !strings.Contains(got, "[1] aks-a (rg1, westeurope, Sub One) [azure]") {
		t.Errorf("expected provider tag in listing, got:\n%s", got)
	}
}

func TestUseCommand_AllUnsupported(t *testing.T) {
	aws := &fakeProvider{name: "aws", profiles: []string{"dev"}}
	buf := setupTest(t, aws)

	if err := runRoot(t, "use", "--all"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "not supported") {
		t.Errorf("expected unsupported message, got:\n%s", buf.String())
	}
}

func TestUseCommand_RequiresProfileWithoutAll(t *testing.T) {
	setupTest(t, &fakeProvider{name: "aws"})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	t.Cleanup(func() { rootCmd.SilenceUsage = false; rootCmd.SilenceErrors = false })

	if err := runRoot(t, "use"); err == nil {
		t.Fatal("expected an error when no profile and no --all is given")
	}
}
