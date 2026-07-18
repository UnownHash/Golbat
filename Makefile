FLAGS = go_json

# Alternative to switch in the sonic json library

#ARCH=$(shell arch)
#ifeq ($(ARCH),x86_64)
#FLAGS := sonic
#else
#FLAGS := go_json
#endif

golbat: FORCE
	go build -tags $(FLAGS) golbat

# --- Profile-guided optimization --------------------------------------------
# Go (>= 1.21) automatically applies ./default.pgo when building the golbat
# main package — locally and in the Docker build (which copies the repo). So
# committing default.pgo makes every build profile-guided; no flag needed.
# Refresh it occasionally (game updates shift hot paths), then commit:
#
#   make pgo-capture                       # reads port + api_secret from config.toml
#   GOLBAT_URL=https://host:9001 GOLBAT_SECRET=... make pgo-capture   # or override
#
# Port and secret are auto-detected from config.toml so a local operator can
# just `make pgo-capture`; any of GOLBAT_URL / GOLBAT_HOST / GOLBAT_PORT /
# GOLBAT_SECRET / CONFIG override the detection.
CONFIG ?= config.toml
DETECTED_PORT := $(shell grep -E '^[[:space:]]*port[[:space:]]*=' $(CONFIG) 2>/dev/null | head -1 | sed -E 's/^[^=]*=[[:space:]]*([0-9]+).*/\1/')
DETECTED_SECRET := $(shell grep -E '^[[:space:]]*api_secret[[:space:]]*=' $(CONFIG) 2>/dev/null | head -1 | sed -E 's/^[^=]*=[[:space:]]*"([^"]*)".*/\1/')
GOLBAT_HOST ?= 127.0.0.1
GOLBAT_PORT ?= $(if $(DETECTED_PORT),$(DETECTED_PORT),9001)
GOLBAT_SECRET ?= $(DETECTED_SECRET)
GOLBAT_URL ?= http://$(GOLBAT_HOST):$(GOLBAT_PORT)
PGO_SECONDS ?= 120

pgo-config: FORCE  ## show the URL/secret pgo-capture will use (from config.toml)
	@echo "config file : $(CONFIG)"
	@echo "capture URL : $(GOLBAT_URL)"
	@echo "secret      : $(if $(GOLBAT_SECRET),(set, $(shell printf %s '$(GOLBAT_SECRET)' | wc -c | tr -d ' ') chars),(none))"

pgo-capture: FORCE  ## capture a CPU profile from a running golbat into default.pgo
	curl -fsS $(if $(GOLBAT_SECRET),-H "X-Golbat-Secret: $(GOLBAT_SECRET)") \
	  "$(GOLBAT_URL)/debug/pprof/profile?seconds=$(PGO_SECONDS)" -o default.pgo.tmp
	mv default.pgo.tmp default.pgo
	@echo "captured default.pgo ($$(du -h default.pgo | cut -f1)) from $(GOLBAT_URL) — commit it to apply to all builds"

pgo-status: FORCE  ## report whether builds will be profile-guided
	@if [ -f default.pgo ]; then \
	  echo "default.pgo present ($$(du -h default.pgo | cut -f1)) — builds are profile-guided"; \
	else \
	  echo "no default.pgo — builds are NOT profile-guided (run make pgo-capture against prod)"; \
	fi

FORCE: ;