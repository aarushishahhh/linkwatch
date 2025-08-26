# Used claude sonnet 4 for help with this
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache gcc musl-dev sqlite-dev

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -o linkwatch main.go

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata sqlite
WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/linkwatch .

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/healthz || exit 1

# Run the binary
CMD ["./linkwatch"]