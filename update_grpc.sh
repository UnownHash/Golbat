protoc --go_out=. --go_opt=paths=source_relative \
    --go_opt=Mvbase.proto=github.com/unownhash/golbat/grpc \
    --go-vtproto_out=. \
    --plugin protoc-gen-go="/Users/james/go/bin/protoc-gen-go" \
    --plugin protoc-gen-go-grpc="/Users/james/go/bin/protoc-gen-go-grpc" \
    --plugin protoc-gen-go-vtproto="/Users/james/go/bin/protoc-gen-go-vtproto" \
    --go-vtproto_opt=paths=source_relative \
    grpc/raw_receiver.proto
    #--go-grpc_out=. --go-grpc_opt=paths=source_relative \
#protoc --go_out=. --go_opt=paths=source_relative \
#    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
#    --go_opt=Mvbase.proto=github.com/unownhash/golbat/grpc \
#    grpc/pokemon_api.proto