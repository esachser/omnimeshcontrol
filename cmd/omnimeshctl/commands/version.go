package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

const version = "0.1.0-dev"

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("omnimeshctl version %s\n", version)
		},
	}
}
