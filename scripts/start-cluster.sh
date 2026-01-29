#!/bin/bash
# Helios 3-Node Cluster Startup Script

set -e

echo "Starting Helios 3-Node Cluster..."

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Clean up previous data
echo "Cleaning up previous cluster data..."
rm -rf ./data/node1 ./data/node2 ./data/node3

# Create data directories
mkdir -p ./data/node1/raft
mkdir -p ./data/node2/raft
mkdir -p ./data/node3/raft

# Build the binary
echo "Building helios-atlasd..."
make build-atlasd || go build -o bin/helios-atlasd cmd/helios-atlasd/main.go

# Start Node 1
echo -e "${GREEN}Starting Node 1 (Leader candidate)...${NC}"
./bin/helios-atlasd \
  -data-dir=./data/node1 \
  -listen=:6379 \
  -raft=true \
  -raft-node-id=node-1 \
  -raft-addr=127.0.0.1:7000 \
  -raft-data-dir=./data/node1/raft \
  > ./data/node1/atlasd.log 2>&1 &

NODE1_PID=$!
echo "Node 1 started (PID: $NODE1_PID) - Client: :6379, Raft: :7000"

# Wait a bit for node 1 to initialize
sleep 2

# Start Node 2
echo -e "${GREEN}Starting Node 2...${NC}"
./bin/helios-atlasd \
  -data-dir=./data/node2 \
  -listen=:6380 \
  -raft=true \
  -raft-node-id=node-2 \
  -raft-addr=127.0.0.1:7001 \
  -raft-data-dir=./data/node2/raft \
  > ./data/node2/atlasd.log 2>&1 &

NODE2_PID=$!
echo "Node 2 started (PID: $NODE2_PID) - Client: :6380, Raft: :7001"

# Wait a bit for node 2 to initialize
sleep 2

# Start Node 3
echo -e "${GREEN}Starting Node 3...${NC}"
./bin/helios-atlasd \
  -data-dir=./data/node3 \
  -listen=:6381 \
  -raft=true \
  -raft-node-id=node-3 \
  -raft-addr=127.0.0.1:7002 \
  -raft-data-dir=./data/node3/raft \
  > ./data/node3/atlasd.log 2>&1 &

NODE3_PID=$!
echo "Node 3 started (PID: $NODE3_PID) - Client: :6381, Raft: :7002"

# Save PIDs for cleanup
echo "$NODE1_PID" > ./data/cluster.pids
echo "$NODE2_PID" >> ./data/cluster.pids
echo "$NODE3_PID" >> ./data/cluster.pids

echo ""
echo -e "${BLUE}================================${NC}"
echo -e "${GREEN}Helios Cluster Started Successfully!${NC}"
echo -e "${BLUE}================================${NC}"
echo ""
echo "Node 1: redis-cli -p 6379"
echo "Node 2: redis-cli -p 6380"
echo "Node 3: redis-cli -p 6381"
echo ""
echo "Logs:"
echo "  Node 1: tail -f ./data/node1/atlasd.log"
echo "  Node 2: tail -f ./data/node2/atlasd.log"
echo "  Node 3: tail -f ./data/node3/atlasd.log"
echo ""
echo "To stop the cluster: ./scripts/stop-cluster.sh"
echo ""
echo "Waiting for leader election (5 seconds)..."
sleep 5

echo ""
echo "Checking cluster status..."
for port in 6379 6380 6381; do
  echo -n "Node on port $port: "
  redis-cli -p $port PING 2>/dev/null || echo "Not responding"
done

echo ""
echo -e "${GREEN}Cluster is ready!${NC}"
