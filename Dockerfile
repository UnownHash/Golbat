# Build image
FROM golang:1.20-alpine as build

WORKDIR /go/src/app
COPY . .

RUN apk add --no-cache \
    # Important: required for go-sqlite3
    gcc \
    # Required for Alpine
    musl-dev

RUN go mod download
RUN CGO_ENABLED=1 \
    go build \
    -ldflags='-s -w -extldflags "-static"' \
    -o /go/bin/golbat

# Now copy it into our base image.
FROM gcr.io/distroless/static-debian11 as runner
COPY --from=build /go/bin/golbat /golbat/
COPY /sql /golbat/sql
COPY /cache /golbat/cache
COPY /logs /golbat/logs

WORKDIR /golbat
CMD ["./golbat"]
