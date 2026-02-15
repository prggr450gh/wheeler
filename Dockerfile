# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies for CGO (required for SQLite)
RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application with CGO enabled for SQLite
RUN CGO_ENABLED=1 go build -o wheeler .


# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Copy app binary, templates, static assets and DB schema from builder
COPY --from=builder /app/wheeler .
COPY --from=builder /app/internal/web/templates ./internal/web/templates
COPY --from=builder /app/internal/web/static ./internal/web/static
COPY --from=builder /app/internal/database/schema.sql ./internal/database/schema.sql
COPY --from=builder /app/internal/database/wheel_strategy_example.sql ./internal/database/wheel_strategy_example.sql
COPY --from=builder /app/internal/database/wheel_strategy_example_clean.sql ./internal/database/wheel_strategy_example_clean.sql

# Create data directory, create unprivileged user, set ownership and ensure binary is executable
RUN mkdir -p /app/data \
 && addgroup -S appuser \
 && adduser -S -G appuser -h /app appuser \
 && chown -R appuser:appuser /app \
 && chmod +x /app/wheeler

# Switch to unprivileged user and run
USER appuser
EXPOSE 8080
CMD ["./wheeler"]
