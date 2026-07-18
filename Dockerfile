# Build image
FROM golang:1.26-alpine AS build

WORKDIR /go/src/app
COPY go.mod go.sum ./
RUN if [ ! -f vendor/modules.txt ]; then go mod download; fi

COPY . .
# `thin`: trimmed pogo schema (pogo/vbase.thin.pb.go) — decode skips the fields
# Golbat never reads. Same decoded values as the full schema; fewer allocations.
RUN CGO_ENABLED=0 go build -tags go_json,thin -o /go/bin/golbat
RUN mkdir /empty-dir

# Now copy it into our base image.
FROM gcr.io/distroless/static-debian11 AS runner
COPY --from=build /go/bin/golbat /golbat/
COPY --from=build /empty-dir /golbat/cache
COPY --from=build /empty-dir /golbat/logs
COPY /sql /golbat/sql

WORKDIR /golbat
CMD ["./golbat"]
