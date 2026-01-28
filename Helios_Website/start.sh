#!/bin/bash
echo "Starting HELIOS Documentation Website..."
echo ""
cd "$(dirname "$0")"
bun run dev
