FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /l10 .

FROM alpine:3.23
RUN addgroup -g 10001 l10 && adduser -D -u 10001 -G l10 l10 && mkdir /data && chown l10:l10 /data
COPY --from=build /l10 /usr/local/bin/l10
USER 10001:10001
ENV PORT=8111 DATA_DIR=/data
EXPOSE 8111
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8111/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/l10"]
