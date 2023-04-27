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

FORCE: ;