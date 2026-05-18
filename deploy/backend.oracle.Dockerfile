# syntax=docker/dockerfile:1.7
# Oracle-enabled build profile. Includes the Oracle Instant Client so the
# godror driver can connect to Oracle databases. Activate with:
#   docker compose --profile oracle up contextforge-oracle
#
# Note: the placeholder oracle driver in this build still returns
# ErrNotEnabled; replace with a godror implementation guarded by a build tag
# (`go build -tags oracle`) before relying on this image in production.

FROM node:20-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM golang:1.25-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends \
        libaio1 unzip wget ca-certificates && rm -rf /var/lib/apt/lists/*
# Oracle Instant Client (replace URL with a mirror you trust)
# RUN mkdir -p /opt/oracle && cd /opt/oracle && \
#     wget -q https://download.oracle.com/otn_software/linux/instantclient/2380000/instantclient-basiclite-linux.x64-23.8.0.25.04.zip && \
#     unzip -q instantclient-*.zip && rm instantclient-*.zip
WORKDIR /src
COPY backend/ ./backend/
WORKDIR /src/backend
RUN go mod download || true
RUN CGO_ENABLED=1 go build -trimpath -tags oracle -o /out/contextforge ./cmd/contextforge

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        libaio1 ca-certificates && rm -rf /var/lib/apt/lists/*
# COPY --from=build /opt/oracle /opt/oracle
# ENV LD_LIBRARY_PATH=/opt/oracle/instantclient_23_8
COPY --from=build /out/contextforge /app/contextforge
COPY --from=web   /web/dist /app/frontend/dist
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["/app/contextforge"]
