FLAGS = go_json

# Alternative to switch in the sonic json library

#ARCH=$(shell arch)
#ifeq ($(ARCH),x86_64)
#FLAGS := sonic
#else
#FLAGS := go_json
#endif

# `thin` selects the trimmed pogo schema (pogo/vbase.thin.pb.go): decode skips
# the ~97% of proto fields Golbat never reads, cutting decode allocations. This
# is the default production build. Use `make golbat-full` for the full schema
# (identical decoded values; only useful for contributors adding field access).
golbat: FORCE
	go build -tags $(FLAGS),thin golbat

golbat-full: FORCE
	go build -tags $(FLAGS) -o golbat golbat

FORCE: ;