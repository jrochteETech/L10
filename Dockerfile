FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY *.sql ./
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /l10 .

FROM alpine:3.23
RUN addgroup -g 10001 l10 && adduser -D -u 10001 -G l10 l10
COPY --from=build --chmod=0755 /l10 /usr/local/bin/l10
USER 10001:10001
ENV PORT=8111
EXPOSE 8111
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8111/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/l10"]
