// Command omnimeshctl is the Omnimesh CLI for interacting with the control plane.
// Usage:
//
//	omnimeshctl apply -f resource.yaml
//	omnimeshctl get service -n production order-service
//	omnimeshctl get namespace production
//	omnimeshctl delete service -n production order-service
//	omnimeshctl delete namespace production [--force]
package main

import (
	"os"

	"github.com/esachser/omnimeshcontrol/cmd/omnimeshctl/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
