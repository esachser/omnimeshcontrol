package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/spf13/cobra"
)

func getCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <kind> [name]",
		Short: "Get one or more resources",
		Long:  "Retrieve resources by kind and optional name. If no name is given, lists all.",
		Example: `  omnimeshctl get namespace
  omnimeshctl get service -n production
  omnimeshctl get service -n production order-service`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := args[0]
			var name string
			if len(args) > 1 {
				name = args[1]
			}
			return runGet(kind, name)
		},
	}
	return cmd
}

func runGet(kind, name string) error {
	client, conn, err := newClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mappedKind := mapKind(kind)

	if name != "" {
		return getOne(ctx, client, mappedKind, name)
	}
	return listAll(ctx, client, mappedKind)
}

func getOne(ctx context.Context, client omnimeshv1.ControlPlaneServiceClient, kind, name string) error {
	resp, err := client.GetResource(ctx, &omnimeshv1.GetResourceRequest{
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
	})
	if err != nil {
		return fmt.Errorf("get %s/%s: %w", kind, name, err)
	}

	return printResourceDetail(resp.Resource)
}

func listAll(ctx context.Context, client omnimeshv1.ControlPlaneServiceClient, kind string) error {
	resp, err := client.ListResources(ctx, &omnimeshv1.ListResourcesRequest{
		Kind:      kind,
		Namespace: namespace,
	})
	if err != nil {
		return fmt.Errorf("list %s: %w", kind, err)
	}

	if len(resp.Resources) == 0 {
		fmt.Printf("No %s resources found in namespace %q\n", kind, namespace)
		return nil
	}

	return printResourceTable(resp.Resources)
}

func printResourceDetail(r *omnimeshv1.Resource) error {
	output := map[string]any{
		"apiVersion": r.ApiVersion,
		"kind":       r.Kind,
		"metadata": map[string]any{
			"name":      r.Metadata.Name,
			"namespace": r.Metadata.Namespace,
			"labels":    r.Metadata.Labels,
		},
	}

	if len(r.Spec) > 0 {
		var spec any
		json.Unmarshal(r.Spec, &spec)
		output["spec"] = spec
	}

	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
	return nil
}

func printResourceTable(resources []*omnimeshv1.Resource) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tNAMESPACE\tKIND")

	for _, r := range resources {
		ns := ""
		if r.Metadata != nil {
			ns = r.Metadata.Namespace
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.Metadata.Name, ns, r.Kind)
	}

	return w.Flush()
}

func mapKind(input string) string {
	switch input {
	case "svc", "service", "services":
		return "Service"
	case "ns", "namespace", "namespaces":
		return "Namespace"
	case "target", "networktarget", "networktargets":
		return "NetworkTarget"
	case "route", "targetroute", "targetroutes":
		return "TargetRoute"
	default:
		return input
	}
}
