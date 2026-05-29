#!/bin/bash

# Build the binary
echo "Building mimicode..."
go build -o mimicode ./cmd/mimicode

# Run with TUI mode
echo "Starting TUI mode..."
./mimicode --tui

# Clean up
rm -f mimicode