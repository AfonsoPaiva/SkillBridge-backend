# ── Stage 1: Build ──────────────────────────────────
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Cache deps
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /skillbridge-server ./cmd/server

# ── Stage 2: Runtime ───────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary
COPY --from=builder /skillbridge-server .

# Copy embedded config files (skills.json, universities, firebase creds)
COPY --from=builder /app/config/skills.json ./config/
COPY --from=builder /app/config/UniversidadesECursos.json ./config/
# Firebase credentials will be mounted via Cloud Run secrets or env
COPY --from=builder /app/config/firebase-credentials.json ./config/

# Copy admin dashboard HTML
COPY --from=builder /app/admin-dashboard.html .

# Create uploads directory
RUN mkdir -p /app/uploads/avatars /app/uploads/projects

# Expose port (Cloud Run uses PORT env var)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s \
  CMD wget -qO- http://localhost:8080/api/guest/stats || exit 1

ENTRYPOINT ["./skillbridge-server"]
