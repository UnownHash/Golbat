protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    --go_opt=Mvbase.proto=github.com/unownhash/golbat/grpc \
    grpc/raw_receiver.proto