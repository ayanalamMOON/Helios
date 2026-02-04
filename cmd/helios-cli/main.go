package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/helios/helios/internal/atlas/protocol"
)

func main() {
	// Parse command-line flags
	host := flag.String("host", "localhost", "ATLAS server host")
	port := flag.Int("port", 6379, "ATLAS server port")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Println("Usage: helios-cli [options] <command> [args...]")
		fmt.Println("\nCommands:")
		fmt.Println("  set <key> <value> [ttl]   - Set a key-value pair")
		fmt.Println("  get <key>                 - Get a value by key")
		fmt.Println("  del <key>                 - Delete a key")
		fmt.Println("  expire <key> <ttl>        - Set expiration for a key")
		fmt.Println("  ttl <key>                 - Get TTL for a key")
		fmt.Println("  addpeer <id> <address>    - Add a peer to the cluster")
		fmt.Println("  removepeer <id>           - Remove a peer from the cluster")
		fmt.Println("  listpeers                 - List all peers in the cluster")
		fmt.Println("\nSharding Commands:")
		fmt.Println("  addnode <id> <address>    - Add a shard node to the cluster")
		fmt.Println("  removenode <id>           - Remove a shard node from the cluster")
		fmt.Println("  listnodes                 - List all shard nodes")
		fmt.Println("  nodeforkey <key>          - Find which node handles a key")
		fmt.Println("  migrate <src> <dst> [pat] - Migrate keys from src to dst node")
		fmt.Println("  clusterstats              - Display cluster statistics")
		fmt.Println("  rebalance                 - Manually trigger cluster rebalancing")
		fmt.Println("  activemigrations          - List all active migrations")
		fmt.Println("  allmigrations             - List all migrations")
		fmt.Println("  cancelmigration <task_id> - Cancel an active migration")
		fmt.Println("\nGraphQL Commands:")
		fmt.Println("  graphql query <query>     - Execute a GraphQL query")
		fmt.Println("  graphql mutate <mutation> - Execute a GraphQL mutation")
		fmt.Println("  graphql introspect        - Introspect the GraphQL schema")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Check if it's a GraphQL command
	if args[0] == "graphql" {
		// Delegate to GraphQL command handler
		graphqlCmd.Execute()
		return
	}

	// Connect to server
	address := net.JoinHostPort(*host, fmt.Sprintf("%d", *port))
	conn, err := net.Dial("tcp", address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to %s: %v\n", address, err)
		os.Exit(1)
	}
	defer conn.Close()

	// Build command
	var cmd *protocol.Command
	commandType := strings.ToUpper(args[0])

	switch commandType {
	case "SET":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "SET requires at least 2 arguments: key and value\n")
			os.Exit(1)
		}
		ttl := int64(0)
		if len(args) >= 4 {
			fmt.Sscanf(args[3], "%d", &ttl)
		}
		cmd = protocol.NewSetCommand(args[1], []byte(args[2]), ttl)

	case "GET":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "GET requires 1 argument: key\n")
			os.Exit(1)
		}
		cmd = protocol.NewGetCommand(args[1])

	case "DEL", "DELETE":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "DEL requires 1 argument: key\n")
			os.Exit(1)
		}
		cmd = protocol.NewDelCommand(args[1])

	case "EXPIRE":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "EXPIRE requires 2 arguments: key and ttl\n")
			os.Exit(1)
		}
		var ttl int64
		fmt.Sscanf(args[2], "%d", &ttl)
		cmd = &protocol.Command{
			Type: "EXPIRE",
			Key:  args[1],
			TTL:  ttl,
		}

	case "TTL":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "TTL requires 1 argument: key\n")
			os.Exit(1)
		}
		cmd = &protocol.Command{
			Type: "TTL",
			Key:  args[1],
		}

	case "ADDPEER":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "ADDPEER requires 2 arguments: peer ID and address\n")
			os.Exit(1)
		}
		cmd = protocol.NewAddPeerCommand(args[1], args[2])

	case "REMOVEPEER":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "REMOVEPEER requires 1 argument: peer ID\n")
			os.Exit(1)
		}
		cmd = protocol.NewRemovePeerCommand(args[1])

	case "LISTPEERS":
		cmd = protocol.NewListPeersCommand()

	case "ADDNODE":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "ADDNODE requires 2 arguments: node ID and address\n")
			os.Exit(1)
		}
		cmd = &protocol.Command{
			Type:  "ADDNODE",
			Key:   args[1], // node ID
			Value: args[2], // address
		}

	case "REMOVENODE":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "REMOVENODE requires 1 argument: node ID\n")
			os.Exit(1)
		}
		cmd = &protocol.Command{
			Type: "REMOVENODE",
			Key:  args[1], // node ID
		}

	case "LISTNODES":
		cmd = &protocol.Command{
			Type: "LISTNODES",
		}

	case "NODEFORKEY":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "NODEFORKEY requires 1 argument: key\n")
			os.Exit(1)
		}
		cmd = &protocol.Command{
			Type: "NODEFORKEY",
			Key:  args[1],
		}

	case "MIGRATE":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "MIGRATE requires at least 2 arguments: source and target node IDs\n")
			os.Exit(1)
		}
		pattern := "*"
		if len(args) >= 4 {
			pattern = args[3]
		}
		cmd = &protocol.Command{
			Type:  "MIGRATE",
			Key:   args[1], // source node
			Value: args[2], // target node
			Extra: map[string]interface{}{
				"pattern": pattern,
			},
		}

	case "CLUSTERSTATS":
		cmd = &protocol.Command{
			Type: "CLUSTERSTATS",
		}

	case "REBALANCE":
		cmd = &protocol.Command{
			Type: "REBALANCE",
		}

	case "ACTIVEMIGRATIONS":
		cmd = &protocol.Command{
			Type: "ACTIVEMIGRATIONS",
		}

	case "ALLMIGRATIONS":
		cmd = &protocol.Command{
			Type: "ALLMIGRATIONS",
		}

	case "CANCELMIGRATION":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "CANCELMIGRATION requires 1 argument: task ID\n")
			os.Exit(1)
		}
		cmd = &protocol.Command{
			Type: "CANCELMIGRATION",
			Key:  args[1], // task ID
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", commandType)
		os.Exit(1)
	}

	// Serialize and send command
	cmdStr, err := protocol.Serialize(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serialize command: %v\n", err)
		os.Exit(1)
	}

	if _, err := fmt.Fprintf(conn, "%s\n", cmdStr); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to send command: %v\n", err)
		os.Exit(1)
	}

	// Read response
	reader := bufio.NewReader(conn)
	respLine, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read response: %v\n", err)
		os.Exit(1)
	}

	// Parse response
	var resp protocol.Response
	if err := json.Unmarshal([]byte(respLine), &resp); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse response: %v\n", err)
		os.Exit(1)
	}

	// Display response
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	if resp.Value != "" {
		// Decode base64 value
		fmt.Println(resp.Value)
	} else if resp.Extra != nil {
		// Pretty print JSON
		data, _ := json.MarshalIndent(resp.Extra, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Println("OK")
	}
}
