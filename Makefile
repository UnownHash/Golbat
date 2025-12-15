golbat: FORCE
	GOEXPERIMENT=jsonv2,greenteagc go build golbat

proto: FORCE
	python3 scripts/add_lazy_proto.py
	protoc --go_out=pogo --go_opt=paths=source_relative --go_opt=default_api_level=API_OPAQUE --go_opt=Mvbase.proto=golbat/pogo vbase.proto

proto-lazy-all: FORCE
	python3 scripts/add_lazy_proto.py --all
	protoc --go_out=pogo --go_opt=paths=source_relative --go_opt=default_api_level=API_OPAQUE --go_opt=Mvbase.proto=golbat/pogo vbase.proto

FORCE: ;