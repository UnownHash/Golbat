#!/bin/bash
set -e

# Add [lazy = true] annotations to unused proto fields
echo "Adding lazy annotations to proto..."
python3 scripts/add_lazy_proto.py

# Generate Go code from proto
echo "Generating Go code..."
protoc --go_out=pogo --go_opt=paths=source_relative --go_opt=default_api_level=API_OPAQUE --go_opt=Mvbase.proto=golbat/pogo \
    vbase.proto

echo "Done!"
