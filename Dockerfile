# Build image
FROM golang:1.23-alpine AS build

WORKDIR /go/src/app
COPY go.mod go.sum ./
RUN if [ ! -f vendor/modules.txt ]; then go mod download; fi

COPY . .
RUN CGO_ENABLED=0 go build -tags go_json -o /go/bin/golbat
RUN mkdir /empty-dir

# Now copy it into our base image.
FROM gcr.io/distroless/static-debian11 AS runner
COPY --from=build /go/bin/golbat /golbat/
COPY --from=build /empty-dir /golbat/cache
COPY --from=build /empty-dir /golbat/logs
COPY /sql /golbat/sql

WORKDIR /golbat
CMD ["./golbat"]
