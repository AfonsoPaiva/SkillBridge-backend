# ── Build stage ───────────────────────────────────────────
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Cache deps
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Ensure docs package is available and tidy
RUN go mod tidy

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server

# ── Runtime stage ─────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/server .
COPY --from=builder /app/config ./config
COPY --from=builder /app/docs ./docs
COPY --from=builder /app/admin-dashboard.html .

# Create uploads directory
RUN mkdir -p /app/uploads

EXPOSE 8080 

CMD ["./server"]