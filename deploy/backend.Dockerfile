# syntax=docker/dockerfile:1.7
# ---- Stage 1: build the React SPA ----
FROM node:20-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# ---- Stage 2: build the Go backend ----
FROM golang:1.25-alpine AS build
RUN apk add --no-cache build-base git
WORKDIR /src
COPY backend/go.mod backend/go.sum* ./backend/
WORKDIR /src/backend
RUN go mod download || true
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" \
        -o /out/contextforge ./cmd/contextforge

# ---- Stage 3: runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/contextforge /app/contextforge
COPY --from=web /web/dist /app/frontend/dist
EXPOSE 8080
USER nobody
ENTRYPOINT ["/app/contextforge"]
