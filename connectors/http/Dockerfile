FROM golang:1.26.10-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o http-connector ./connectors/http

FROM alpine:3.22

ARG APP_USER=artificialflow
ARG APP_UID=10001
ARG APP_GID=10001

WORKDIR /app

RUN addgroup -S -g "${APP_GID}" "${APP_USER}" \
    && adduser -S -D -H -u "${APP_UID}" -G "${APP_USER}" "${APP_USER}"
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates
COPY --from=builder /app/http-connector .
USER ${APP_USER}

LABEL org.opencontainers.image.title="ArtificialFlow HTTP Connector" \
      org.opencontainers.image.description="Outbound HTTP connector worker" \
      org.opencontainers.image.source="https://github.com/artificialflow/artificialflow" \
      org.opencontainers.image.licenses="MIT"

CMD ["./http-connector"]
