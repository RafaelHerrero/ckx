package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search for AWS profiles and Azure subscriptions by name",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.ToLower(args[0])

		entries, errs := collectProfiles(context.Background())
		for _, err := range errs {
			fmt.Fprintln(outputWriter, "Warning:", err)
		}
		if len(entries) == 0 && len(errs) > 0 {
			fmt.Fprintln(outputWriter, "Failed to list profiles")
			return
		}

		var matches []profileEntry
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.name), query) {
				matches = append(matches, e)
			}
		}

		if len(matches) == 0 {
			fmt.Fprintf(outputWriter, "No profiles found matching %q\n", args[0])
			return
		}

		fmt.Fprintf(outputWriter, "Profiles matching %q:\n", args[0])
		for _, e := range matches {
			fmt.Fprintf(outputWriter, "%s (%s)\n", e.name, e.provider.Name())
		}
	},
}

func init() {
	rootCmd.AddCommand(searchCmd)
}
