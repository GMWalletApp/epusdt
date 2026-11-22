FROM golang:alpine AS builder

RUN apk add --no-cache --update git build-base
ENV CGO_ENABLED=0

WORKDIR /app

COPY . /app

ARG BUILD_VERSION=0.0.0-dev
ARG BUILD_COMMIT=none
ARG BUILD_DATE=unknown

WORKDIR /app/src
RUN go mod download
RUN go build -trimpath \
  -ldflags="-s -w -X github.com/GMWalletApp/epusdt/config.BuildVersion=${BUILD_VERSION} -X github.com/GMWalletApp/epusdt/config.BuildCommit=${BUILD_COMMIT} -X github.com/GMWalletApp/epusdt/config.BuildDate=${BUILD_DATE} -X github.com/GMWalletApp/epusdt/config.BuildType=container" \
  -o /app/epusdt .

WORKDIR /app/chainscanner
RUN go mod download
RUN go build -trimpath -ldflags="-s -w" -o /out/chainscanner ./cmd/chainscanner

FROM alpine:latest AS runner
ENV TZ=Asia/Shanghai
RUN apk --no-cache add ca-certificates tzdata
ARG API_RATE_URL=""

WORKDIR /app
COPY --from=builder /app/src/.env.example /app/.env.example
COPY --from=builder /app/src/.env.example /app/.env
RUN if [ -n "$API_RATE_URL" ]; then \
      sed -i "s|^api_rate_url=.*$|api_rate_url=${API_RATE_URL}|" /app/.env.example /app/.env; \
    fi
COPY --from=builder /app/epusdt /app/epusdt
COPY --from=builder /out/chainscanner /app/chainscanner
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh /app/epusdt /app/chainscanner

VOLUME ["/app/runtime"]
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["./epusdt", "http", "start"]
