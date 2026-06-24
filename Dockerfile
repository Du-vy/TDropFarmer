FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tdropfarmer ./cmd/tdropfarmer

FROM alpine:3.22

RUN addgroup -S tdropfarmer && adduser -S -G tdropfarmer tdropfarmer
WORKDIR /app
COPY --from=build /out/tdropfarmer /usr/local/bin/tdropfarmer
USER tdropfarmer

ENTRYPOINT ["tdropfarmer"]
CMD ["run", "--config", "/app/config.json"]
