package commands

import (
	"context"
	"fmt"
	"time"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/spf13/cobra"
)

func deleteCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "delete <kind> <name>",
		Short: "Delete a resource",
		Long:  "Delete a resource by kind and name.",
		Example: `  omnimeshctl delete service -n production order-service
  omnimeshctl delete namespace production --force`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := mapKind(args[0])
			name := args[1]
			return runDelete(kind, name, force)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "force deletion (e.g., skip namespace non-empty check)")

	return cmd
}

func runDelete(kind, name string, force bool) error {
	client, conn, err := newClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = client.DeleteResource(ctx, &omnimeshv1.DeleteResourceRequest{
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
		Force:     force,
	})
	if err != nil {
		return fmt.Errorf("delete %s/%s: %w", kind, name, err)
	}

	fmt.Printf("%s/%s deleted\n", kind, name)
	return nil
}
