#!/bin/bash
# Helios Cluster Shutdown Script

set -e

echo "Stopping Helios Cluster..."

if [ -f ./data/cluster.pids ]; then
  while read pid; do
    if kill -0 $pid 2>/dev/null; then
      echo "Stopping process $pid..."
      kill -TERM $pid
      # Wait for graceful shutdown
      sleep 2
      # Force kill if still running
      if kill -0 $pid 2>/dev/null; then
        echo "Force killing process $pid..."
        kill -9 $pid
      fi
    fi
  done < ./data/cluster.pids

  rm ./data/cluster.pids
  echo "Cluster stopped successfully!"
else
  echo "No cluster.pids file found. Attempting to find processes..."
  pkill -f "helios-atlasd" || echo "No running processes found"
fi

echo "Done!"
