package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

func applyCmd() *cobra.Command {
	var filename string

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a resource from file",
		Long:  "Create or update a resource from a YAML or JSON file.",
		Example: `  omnimeshctl apply -f service.yaml
  omnimeshctl apply -f namespace.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApply(filename)
		},
	}

	cmd.Flags().StringVarP(&filename, "file", "f", "", "filename to apply (required)")
	cmd.MarkFlagRequired("file")

	return cmd
}

func runApply(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// Convert YAML to JSON.
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}

	// Parse into a proto-like structure.
	var rawResource struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name      string            `json:"name"`
			Namespace string            `json:"namespace"`
			Labels    map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(jsonData, &rawResource); err != nil {
		return fmt.Errorf("unmarshal resource: %w", err)
	}

	// Use namespace flag if not set in resource.
	if rawResource.Metadata.Namespace == "" {
		rawResource.Metadata.Namespace = namespace
	}

	client, conn, err := newClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.ApplyResource(ctx, &omnimeshv1.ApplyResourceRequest{
		Resource: &omnimeshv1.Resource{
			ApiVersion: rawResource.APIVersion,
			Kind:       rawResource.Kind,
			Metadata: &omnimeshv1.ResourceMetadata{
				Name:      rawResource.Metadata.Name,
				Namespace: rawResource.Metadata.Namespace,
				Labels:    rawResource.Metadata.Labels,
			},
			Spec: rawResource.Spec,
		},
	})
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}

	fmt.Printf("%s/%s applied\n", resp.Resource.Kind, resp.Resource.Metadata.Name)
	return nil
}
