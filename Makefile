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
#   GOLBAT_URL=https://host:9001 GOLBAT_SECRET=... make pgo-capture
#
GOLBAT_URL ?= http://127.0.0.1:9001
GOLBAT_SECRET ?=
PGO_SECONDS ?= 120

pgo-capture: FORCE  ## capture a CPU profile from a running golbat into default.pgo
	curl -fsS $(if $(GOLBAT_SECRET),-H "X-Golbat-Secret: $(GOLBAT_SECRET)") \
	  "$(GOLBAT_URL)/debug/pprof/profile?seconds=$(PGO_SECONDS)" -o default.pgo.tmp
	mv default.pgo.tmp default.pgo
	@echo "captured default.pgo ($$(du -h default.pgo | cut -f1)) — commit it to apply to all builds"

pgo-status: FORCE  ## report whether builds will be profile-guided
	@if [ -f default.pgo ]; then \
	  echo "default.pgo present ($$(du -h default.pgo | cut -f1)) — builds are profile-guided"; \
	else \
	  echo "no default.pgo — builds are NOT profile-guided (run make pgo-capture against prod)"; \
	fi

FORCE: ;