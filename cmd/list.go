package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List available AWS profiles and Azure subscriptions",
	Run: func(cmd *cobra.Command, args []string) {
		entries, errs := collectProfiles(context.Background())
		for _, err := range errs {
			fmt.Fprintln(cmd.ErrOrStderr(), "Warning:", err)
		}
		if len(entries) == 0 && len(errs) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Error: failed to list profiles")
			return
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Available profiles:")
		for _, e := range entries {
			fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", e.name, e.provider.Name())
		}
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
