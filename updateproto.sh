protoc --go_out=pogo --go_opt=paths=source_relative --go_opt=default_api_level=API_OPAQUE --go_opt=Mvbase.proto=golbat/pogo \
    vbase.proto
