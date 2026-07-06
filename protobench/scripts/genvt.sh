#!/bin/bash
# Generates protobench/pogovt: a pruned (GMO+Encounter closure), renamed
# (POGOProtos.Rpc.Vt) copy of vbase.proto with open-API structs plus
# vtprotobuf unmarshal + pool code. The rename keeps its registrations from
# colliding with protobench/pogo in the same binary.
set -euo pipefail
cd "$(dirname "$0")/.."

PROTO_SRC="${PROTO_SRC:-$HOME/dev/ProtoMirror/vbase.proto}"

command -v protoc >/dev/null || { echo "protoc not found (brew install protobuf)" >&2; exit 1; }
[ -f "$PROTO_SRC" ] || { echo "proto source not found: $PROTO_SRC (set PROTO_SRC)" >&2; exit 1; }

mkdir -p bin build pogovt
GOBIN="$(pwd)/bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
GOBIN="$(pwd)/bin" go install github.com/planetscale/vtprotobuf/cmd/protoc-gen-go-vtproto@v0.6.0

python3 scripts/prune_proto.py --proto "$PROTO_SRC" --out build/vbasevt.proto \
  --roots GetMapObjectsOutProto,EncounterOutProto --package POGOProtos.Rpc.Vt

# High-frequency message types get sync.Pool support; UnmarshalVT allocates
# submessages of pooled types from their pools.
POOL_TYPES=(GetMapObjectsOutProto ClientMapCellProto PokemonFortProto WildPokemonProto
  PokemonProto PokemonDisplayProto NearbyPokemonProto MapPokemonProto
  ClientWeatherProto EncounterOutProto
  RaidInfoProto GymDisplayProto PokestopIncidentDisplayProto ClientSpawnPointProto
  PokemonSummaryFortProto StationProto CaptureProbabilityProto
  GameplayWeatherProto DisplayWeatherProto)
POOL_OPTS=()
for t in "${POOL_TYPES[@]}"; do POOL_OPTS+=("--go-vtproto_opt=pool=protobench/pogovt.$t"); done

PATH="$(pwd)/bin:$PATH" protoc -I build \
  --go_out=pogovt --go_opt=paths=source_relative --go_opt=Mvbasevt.proto=protobench/pogovt \
  --go-vtproto_out=pogovt --go-vtproto_opt=paths=source_relative \
  --go-vtproto_opt=Mvbasevt.proto=protobench/pogovt \
  --go-vtproto_opt=features=unmarshal+pool \
  "${POOL_OPTS[@]}" \
  vbasevt.proto

go mod tidy
echo "generated: $(ls pogovt/)"
