#!/usr/bin/env python3
"""
Automatically analyze Go code usage and add [lazy = true] to proto fields
that are not accessed in the codebase.

This script:
1. Scans Go files to find all getter methods being used
2. Parses the proto file to find message definitions and enums
3. Identifies nested message fields that are never accessed
4. Adds [lazy = true] annotation to those fields (only for submessage types)

Run before protoc generation to optimize unmarshaling performance.
"""

import re
import subprocess
import os
import sys
from collections import defaultdict


# Primitive types that cannot be lazy
PRIMITIVES = {
    'string', 'int32', 'int64', 'uint32', 'uint64',
    'sint32', 'sint64', 'fixed32', 'fixed64',
    'sfixed32', 'sfixed64', 'bool', 'float', 'double', 'bytes'
}

# Global set of enum names (populated during parsing)
ENUM_NAMES = set()


def get_script_dir():
    return os.path.dirname(os.path.abspath(__file__))


def get_project_root():
    return os.path.dirname(get_script_dir())


def get_used_getters(project_root):
    """Extract all getter methods used in Go code"""
    result = subprocess.run(
        ["grep", "-rhoE", r"\.Get[A-Z][a-zA-Z0-9_]*\(\)", "--include=*.go", "."],
        cwd=project_root,
        capture_output=True,
        text=True
    )
    getters = set()
    for line in result.stdout.strip().split('\n'):
        if line:
            match = re.match(r'\.Get([A-Z][a-zA-Z0-9_]*)\(\)', line)
            if match:
                getters.add(match.group(1))
    return getters


def get_used_proto_types(project_root):
    """Extract all pogo.* proto types used in Go code that we unmarshal (receive).
    
    Only include OutProto types (responses) and their nested message types,
    since lazy only benefits messages we unmarshal, not ones we create/send.
    """
    result = subprocess.run(
        ["grep", "-rhoE", r"pogo\.[A-Z][a-zA-Z0-9_]*", "--include=*.go", "."],
        cwd=project_root,
        capture_output=True,
        text=True
    )
    types = set()
    for line in result.stdout.strip().split('\n'):
        if line:
            match = re.match(r'pogo\.([A-Z][a-zA-Z0-9_]*)', line)
            if match:
                type_name = match.group(1)
                # Filter out enum values (usually SCREAMING_CASE or have underscores)
                if '_' in type_name:
                    continue
                # Only include response protos (OutProto) and common nested types
                # Skip request protos (just "Proto" without "Out") as we create those, not unmarshal
                if (type_name.endswith('OutProto') or 
                    type_name.endswith('Proto') and not type_name.endswith('OutProto') and 
                    not type_name.startswith('Get') and
                    not type_name.startswith('Set') and
                    not type_name.startswith('Update') and
                    not type_name.startswith('Create') and
                    not type_name.startswith('Delete') and
                    not type_name.startswith('Send') and
                    not type_name.startswith('Submit')):
                    types.add(type_name)
    return types


def snake_to_pascal(name):
    """Convert snake_case to PascalCase"""
    return ''.join(word.capitalize() for word in name.split('_'))


def parse_proto_enums(proto_file):
    """First pass: extract all enum names from proto file"""
    enums = set()
    
    with open(proto_file, 'r') as f:
        for line in f:
            line = line.strip()
            # Match: enum EnumName {
            enum_match = re.match(r'^enum\s+([A-Za-z0-9_]+)\s*\{', line)
            if enum_match:
                enums.add(enum_match.group(1))
    
    return enums


def parse_proto_messages(proto_file, enum_names):
    """Parse proto file to extract message definitions with their fields"""
    messages = {}
    message_names = set()
    current_message = None
    current_fields = []
    brace_depth = 0

    # First pass: collect all message names
    with open(proto_file, 'r') as f:
        for line in f:
            line = line.strip()
            msg_match = re.match(r'^message\s+([A-Za-z0-9_]+)\s*\{', line)
            if msg_match:
                message_names.add(msg_match.group(1))

    # Second pass: parse message fields
    with open(proto_file, 'r') as f:
        for line_num, line in enumerate(f, 1):
            line = line.strip()

            # Start of message
            msg_match = re.match(r'^message\s+([A-Za-z0-9_]+)\s*\{', line)
            if msg_match and brace_depth == 0:
                current_message = msg_match.group(1)
                current_fields = []
                brace_depth = 1
                continue

            if current_message:
                brace_depth += line.count('{') - line.count('}')

                if brace_depth == 0:
                    messages[current_message] = current_fields
                    current_message = None
                    current_fields = []
                    continue

                # Only parse fields at depth 1 (direct message fields)
                if brace_depth == 1:
                    # Skip if already has lazy annotation
                    if '[lazy = true]' in line:
                        continue
                    
                    # Skip repeated fields (lazy doesn't apply)
                    if line.startswith('repeated '):
                        continue

                    # Match field: type field_name = number;
                    field_match = re.match(
                        r'^([A-Za-z0-9_\.]+)\s+([a-z_][a-z0-9_]*)\s*=\s*(\d+)\s*;',
                        line
                    )
                    if field_match:
                        field_type = field_match.group(1)
                        field_name = field_match.group(2)
                        field_num = int(field_match.group(3))

                        # Only include if it's a message type (not primitive, not enum)
                        # A type is a message if:
                        # 1. It's in our message_names set, OR
                        # 2. It ends with 'Proto' (convention for message types)
                        is_message = (
                            field_type in message_names or
                            field_type.endswith('Proto')
                        ) and (
                            field_type not in PRIMITIVES and
                            field_type not in enum_names
                        )

                        if is_message:
                            current_fields.append({
                                'name': field_name,
                                'type': field_type,
                                'number': field_num,
                                'line_num': line_num
                            })

    return messages


def add_lazy_annotations(proto_file, lazy_fields_by_message, dry_run=False):
    """Add [lazy = true] to specified fields in the proto file.
    
    lazy_fields_by_message: dict mapping message_name -> set of field_names
    """
    with open(proto_file, 'r') as f:
        lines = f.readlines()

    changes = 0
    current_message = None
    brace_depth = 0

    for i, line in enumerate(lines):
        stripped = line.strip()
        
        # Track which message we're in
        msg_match = re.match(r'^message\s+([A-Za-z0-9_]+)\s*\{', stripped)
        if msg_match and brace_depth == 0:
            current_message = msg_match.group(1)
            brace_depth = 1
            continue
        
        if current_message:
            brace_depth += stripped.count('{') - stripped.count('}')
            
            if brace_depth == 0:
                current_message = None
                continue
            
            # Only modify fields at depth 1 within target messages
            if brace_depth == 1 and current_message in lazy_fields_by_message:
                if '[lazy = true]' in line:
                    continue
                
                # Check each lazy field for this message
                for field_name in lazy_fields_by_message[current_message]:
                    pattern = re.compile(
                        rf'^(\s*[A-Za-z0-9_]+\s+)({field_name}\s*=\s*\d+)(\s*;)\s*$'
                    )
                    match = pattern.match(line)
                    if match:
                        prefix = match.group(1)
                        field_def = match.group(2)
                        semicolon = match.group(3)
                        lines[i] = f"{prefix}{field_def} [lazy = true]{semicolon}\n"
                        changes += 1
                        break

    if not dry_run and changes > 0:
        with open(proto_file, 'w') as f:
            f.writelines(lines)

    return changes


def main():
    project_root = get_project_root()
    proto_file = os.path.join(project_root, "vbase.proto")

    if not os.path.exists(proto_file):
        print(f"Error: Proto file not found: {proto_file}", file=sys.stderr)
        sys.exit(1)

    dry_run = '--dry-run' in sys.argv
    verbose = '--verbose' in sys.argv or '-v' in sys.argv

    if verbose:
        print(f"Project root: {project_root}")
        print(f"Proto file: {proto_file}")
        print(f"Dry run: {dry_run}")
        print()

    # Get used getters from Go code
    used_getters = get_used_getters(project_root)
    if verbose:
        print(f"Found {len(used_getters)} unique getter methods used in Go code")

    # Dynamically discover which proto types are used in Go code
    key_messages = get_used_proto_types(project_root)
    if verbose:
        print(f"Found {len(key_messages)} proto types used in Go code")

    # Parse enums first
    enum_names = parse_proto_enums(proto_file)
    if verbose:
        print(f"Found {len(enum_names)} enum definitions")

    # Filter key_messages to only include actual messages (not enums)
    key_messages = key_messages - enum_names
    if verbose:
        print(f"After filtering enums: {len(key_messages)} message types")

    # Parse proto file
    messages = parse_proto_messages(proto_file, enum_names)
    if verbose:
        print(f"Parsed {len(messages)} message definitions from proto file")
        print()

    # Find lazy candidates per message
    lazy_candidates_by_message = defaultdict(set)
    total_candidates = 0

    for msg_name in key_messages:
        if msg_name not in messages:
            continue

        fields = messages[msg_name]

        for field in fields:
            pascal_name = snake_to_pascal(field['name'])
            is_used = pascal_name in used_getters

            if verbose:
                status = "USED" if is_used else "LAZY"
                print(f"  {msg_name}.{field['name']} ({field['type']}): {status}")

            if not is_used:
                lazy_candidates_by_message[msg_name].add(field['name'])
                total_candidates += 1

    if verbose:
        print()
        print(f"Total lazy candidates: {total_candidates}")
        print()

    # Add lazy annotations
    if total_candidates > 0:
        changes = add_lazy_annotations(proto_file, lazy_candidates_by_message, dry_run)
        action = "Would add" if dry_run else "Added"
        print(f"{action} [lazy = true] to {changes} fields")
    else:
        print("No lazy candidates found")

    return 0


if __name__ == '__main__':
    sys.exit(main())
