package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ckx",
	Short: "Switch between AWS (EKS) and Azure (AKS) clusters: log in and update kubeconfig",
	Long: `Switch between AWS (EKS) and Azure (AKS) clusters: log in and update kubeconfig.

Run without a subcommand to pick a profile interactively with fzf; the
selection is then passed to 'ckx use'.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		entry, ok, err := pickProfile(ctx)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "Error:", err)
			os.Exit(1)
		}
		if !ok {
			return
		}
		runUse(ctx, entry.name, entry.provider.Name(), false)
	},
}

func init() {
	rootCmd.Flags().BoolVar(&exportFlag, "export", false, "Output shell commands for eval (export AWS_PROFILE / AZURE_SUBSCRIPTION_ID)")
}

func profileLabel(e profileEntry) string {
	return fmt.Sprintf("%s (%s)", e.name, e.provider.Name())
}

// pickProfile lets the user choose a profile with fzf. ok is false when the
// user cancelled the selection.
func pickProfile(ctx context.Context) (profileEntry, bool, error) {
	if _, err := exec.LookPath("fzf"); err != nil {
		return profileEntry{}, false, fmt.Errorf("fzf not found in PATH; install it or use 'ckx list' and 'ckx use <profile>'")
	}

	entries, errs := collectProfiles(ctx)
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, "Warning:", err)
	}
	if len(entries) == 0 {
		return profileEntry{}, false, fmt.Errorf("no profiles found")
	}

	byLabel := make(map[string]profileEntry, len(entries))
	var input bytes.Buffer
	for _, e := range entries {
		label := profileLabel(e)
		byLabel[label] = e
		input.WriteString(label + "\n")
	}

	fzf := execCommand("fzf", "--prompt", "profile> ", "--height", "40%", "--reverse")
	fzf.Stdin = &input
	fzf.Stderr = os.Stderr
	out, err := fzf.Output()
	if err != nil {
		var exitErr *exec.ExitError
		// 1: no match, 130: cancelled with Esc/Ctrl-C
		if errors.As(err, &exitErr) && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == 130) {
			return profileEntry{}, false, nil
		}
		return profileEntry{}, false, fmt.Errorf("fzf: %w", err)
	}

	selected := strings.TrimSpace(string(out))
	e, found := byLabel[selected]
	if !found {
		return profileEntry{}, false, fmt.Errorf("unknown selection %q", selected)
	}
	return e, true, nil
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
