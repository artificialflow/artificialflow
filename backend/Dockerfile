FROM golang:1.27.1-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ARG REVISION=unknown
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" -o workflow-command backend/services/workflow-command/cmd/server/main.go

FROM alpine:3.22

ARG VERSION=dev
ARG REVISION=unknown
ARG APP_USER=artificialflow
ARG APP_UID=10001
ARG APP_GID=10001

WORKDIR /app

RUN addgroup -S -g "${APP_GID}" "${APP_USER}" \
    && adduser -S -D -H -u "${APP_UID}" -G "${APP_USER}" "${APP_USER}"
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates
COPY --from=builder /app/workflow-command .
USER ${APP_USER}

EXPOSE 8080

LABEL org.opencontainers.image.title="ArtificialFlow Command Service" \
      org.opencontainers.image.description="Command API service for ArtificialFlow" \
      org.opencontainers.image.source="https://github.com/artificialflow/artificialflow" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

CMD ["./workflow-command"]
