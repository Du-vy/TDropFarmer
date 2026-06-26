FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tdropfarmer ./cmd/tdropfarmer

FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=build /out/tdropfarmer /usr/local/bin/tdropfarmer

VOLUME ["/app/data"]

ENTRYPOINT ["tdropfarmer"]
CMD ["run", "--config", "/app/config.json"]
