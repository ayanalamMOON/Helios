package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/helios/helios/internal/raft"
)

func main() {
	// Command line flags
	outputDir := flag.String("output", "./certs", "Output directory for certificates")
	nodeList := flag.String("nodes", "node-1,node-2,node-3", "Comma-separated list of node IDs")
	hostsList := flag.String("hosts", "", "Comma-separated list of additional hosts (optional)")
	flag.Parse()

	// Parse node IDs
	nodeIDs := strings.Split(*nodeList, ",")
	if len(nodeIDs) == 0 {
		fmt.Fprintf(os.Stderr, "Error: at least one node ID is required\n")
		os.Exit(1)
	}

	// Parse hosts
	hosts := make(map[string][]string)
	if *hostsList != "" {
		hostList := strings.Split(*hostsList, ",")
		// Apply these hosts to all nodes
		for _, nodeID := range nodeIDs {
			hosts[nodeID] = hostList
		}
	}

	fmt.Printf("Generating TLS certificates for Raft cluster...\n")
	fmt.Printf("  Output directory: %s\n", *outputDir)
	fmt.Printf("  Nodes: %s\n", strings.Join(nodeIDs, ", "))

	// Generate certificates
	if err := raft.GenerateClusterCertificates(*outputDir, nodeIDs, hosts); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating certificates: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✓ Certificates generated successfully!\n\n")
	fmt.Printf("Generated files:\n")
	fmt.Printf("  %s/ca.crt       - CA certificate (distribute to all nodes)\n", *outputDir)
	fmt.Printf("  %s/ca.key       - CA private key (keep secure!)\n", *outputDir)

	for _, nodeID := range nodeIDs {
		fmt.Printf("  %s/%s.crt  - Certificate for %s\n", *outputDir, nodeID, nodeID)
		fmt.Printf("  %s/%s.key  - Private key for %s\n", *outputDir, nodeID, nodeID)
	}

	fmt.Printf("\nUsage:\n")
	fmt.Printf("1. Copy ca.crt to all nodes\n")
	fmt.Printf("2. Copy <node-id>.crt and <node-id>.key to each respective node\n")
	fmt.Printf("3. Configure your nodes to use TLS with these certificates\n")
}
