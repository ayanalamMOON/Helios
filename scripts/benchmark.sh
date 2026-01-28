#!/bin/bash
# Benchmark script for HELIOS

set -e

echo "=== HELIOS Benchmark Suite ==="
echo ""

# Check if ATLAS daemon is running
if ! nc -z localhost 6379 2>/dev/null; then
    echo "Error: ATLAS daemon is not running on port 6379"
    exit 1
fi

echo "1. Testing SET operations"
echo "Running 10000 SET operations..."
time for i in {1..10000}; do
    echo '{"cmd":"SET","key":"bench'$i'","value":"data'$i'","ttl":0}' | nc localhost 6379 > /dev/null
done

echo ""
echo "2. Testing GET operations"
echo "Running 10000 GET operations..."
time for i in {1..10000}; do
    echo '{"cmd":"GET","key":"bench'$i'"}' | nc localhost 6379 > /dev/null
done

echo ""
echo "3. Testing mixed operations (70% GET, 30% SET)"
echo "Running 10000 mixed operations..."
time for i in {1..10000}; do
    if [ $((i % 10)) -lt 7 ]; then
        echo '{"cmd":"GET","key":"bench'$((RANDOM % 10000))'"}' | nc localhost 6379 > /dev/null
    else
        echo '{"cmd":"SET","key":"bench'$i'","value":"data'$i'","ttl":0}' | nc localhost 6379 > /dev/null
    fi
done

echo ""
echo "4. Testing TTL operations"
echo "Running 1000 TTL operations..."
time for i in {1..1000}; do
    echo '{"cmd":"EXPIRE","key":"bench'$i'","ttl":60}' | nc localhost 6379 > /dev/null
done

echo ""
echo "=== Benchmark Complete ==="
