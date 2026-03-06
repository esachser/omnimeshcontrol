// Package commands contains all cobra CLI commands for omnimeshctl.
package commands

import (
	"fmt"
	"os"
	"time"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	serverAddr string
	namespace  string
)

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "omnimeshctl",
		Short: "Omnimesh control plane CLI",
		Long:  "omnimeshctl manages services, namespaces, and policies in the Omnimesh service mesh.",
	}

	root.PersistentFlags().StringVar(&serverAddr, "server", envOrDefault("OMNIMESH_SERVER", "localhost:9090"), "gRPC server address")
	root.PersistentFlags().StringVarP(&namespace, "namespace", "n", "default", "namespace")

	root.AddCommand(applyCmd())
	root.AddCommand(getCmd())
	root.AddCommand(deleteCmd())
	root.AddCommand(versionCmd())

	return root
}

// Execute runs the root command.
func Execute() error {
	return rootCmd().Execute()
}

// newClient creates a gRPC client connection to the control plane.
func newClient() (omnimeshv1.ControlPlaneServiceClient, *grpc.ClientConn, error) {
	ctx, cancel := contextWithTimeout()
	defer cancel()
	_ = ctx

	conn, err := grpc.NewClient(serverAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to %s: %w", serverAddr, err)
	}

	return omnimeshv1.NewControlPlaneServiceClient(conn), conn, nil
}

func contextWithTimeout() (interface{ Done() <-chan struct{} }, func()) {
	return newContextWithTimeout()
}

func newContextWithTimeout() (interface{ Done() <-chan struct{} }, func()) {
	type ctxPair struct {
		done   <-chan struct{}
		cancel func()
	}
	// Use a simple approach: context with 30s timeout.
	return nil, func() {} // placeholder, actual context created per-command
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// Suppress unused import.
var _ = time.Second
