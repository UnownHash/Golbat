CORPUS ?= ../corpus-frozen

test: FORCE
	go test ./... && go test -tags protoopaque ./...

# cmd/bench/default.pgo is committed; go build/run applies it automatically
# (Go >= 1.21), so all harness runs are profile-guided. Refresh after big
# reader/engine changes, then commit.
pgo: FORCE
	go run ./cmd/bench -corpus $(CORPUS) -workers 12 -duration 15s -engine hypershim -cpuprofile cmd/bench/default.pgo
	@echo "cmd/bench/default.pgo refreshed — commit it"

FORCE: ;
