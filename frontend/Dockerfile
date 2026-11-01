FROM --platform=$BUILDPLATFORM node:22-alpine AS builder

WORKDIR /app/frontend

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
COPY connectors/internal/common/descriptors.json /app/connectors/internal/common/descriptors.json

ARG VITE_API_URL

ENV VITE_API_URL=$VITE_API_URL

RUN npm run build

FROM nginx:1.31-alpine

ARG VERSION=dev
ARG REVISION=unknown

RUN apk upgrade --no-cache

COPY --from=builder /app/frontend/dist /usr/share/nginx/html
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY frontend/entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh

EXPOSE 80

LABEL org.opencontainers.image.title="ArtificialFlow Frontend" \
      org.opencontainers.image.description="Frontend UI for ArtificialFlow" \
      org.opencontainers.image.source="https://github.com/artificialflow/artificialflow" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

ENTRYPOINT ["/entrypoint.sh"]
CMD ["nginx", "-g", "daemon off;"]
