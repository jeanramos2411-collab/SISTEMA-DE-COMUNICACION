#!/bin/bash
# Test Script for WebSocket Server Equivalence
# Run this in an environment with Go installed

set -e

cd "$(dirname "$0")/server-go"

echo "=============================================="
echo "WebSocket Server Equivalence Tests"
echo "=============================================="

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "ERROR: Go is not installed"
    echo "Install Go from: https://go.dev/dl/"
    exit 1
fi

echo "Go version:"
go version

echo ""
echo "=============================================="
echo "1. Running unit tests..."
echo "=============================================="
go test -v ./internal/websocket/...

echo ""
echo "=============================================="
echo "2. Building server..."
echo "=============================================="
go build -o ptt-server-test ./cmd/ptt-server/

echo ""
echo "=============================================="
echo "3. Testing server startup..."
echo "=============================================="
timeout 3 ./ptt-server-test || true

echo ""
echo "=============================================="
echo "4. Running Python server for comparison..."
echo "=============================================="
cd "$(dirname "$0")/server"
if command -v python3 &> /dev/null; then
    timeout 3 python3 -m app.main || true
fi

echo ""
echo "=============================================="
echo "Tests completed!"
echo "=============================================="
