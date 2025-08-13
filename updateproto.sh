protoc --go_out=pogo --go-vtproto_out=pogo --go_opt=paths=source_relative --go_opt=Mvbase.proto=golbat.com/pogo \
    --plugin protoc-gen-go="/Users/james/go/bin/protoc-gen-go" \
    --plugin protoc-gen-go-vtproto="/Users/james/go/bin/protoc-gen-go-vtproto" \
    --go-vtproto_opt=Mvbase.proto=golbat.com/pogo \
    --go-vtproto_opt=paths=source_relative \
    vbase.proto


#  --plugin protoc-gen-go="${GOBIN}/protoc-gen-go" \
  #