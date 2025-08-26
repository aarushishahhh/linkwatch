# Linkwatch

A tiny HTTP service that registers URLs, periodically checks them, and exposes their status.

## Features

- **URL Registration**: POST endpoints to register URLs for monitoring
- **Background Checking**: Periodic health checks with configurable intervals  
- **Status Tracking**: Store and retrieve check results with timestamps
- **Concurrency Control**: Per-host serialization and configurable max concurrency
- **Retry Logic**: Exponential backoff for 5xx and network errors
- **Idempotency**: Support for idempotency keys to prevent duplicate registrations
- **Cursor Pagination**: Stable pagination for listing endpoints
- **Graceful Shutdown**: Proper cleanup on SIGTERM

## Quick Start

### Local Development

```bash
# Clone the repository
git clone https://github.com/your-username/linkwatch.git
cd linkwatch

# Install dependencies
go mod download

# Run with default SQLite database
go run main.go

# Or with PostgreSQL
export DATABASE_URL="postgres://user:password@localhost:5432/linkwatch?sslmode=disable"
go run main.go
```

### Using Docker

```bash
# Build the image
docker build -t linkwatch .

# Run with SQLite (data persisted in container)
docker run -p 8080:8080 linkwatch

# Run with PostgreSQL
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://user:password@host:5432/linkwatch?sslmode=disable" \
  linkwatch
```

### Using Docker Compose (with PostgreSQL)

```yaml
version: '3.8'
services:
  app:
    build: .
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://linkwatch:password@db:5432/linkwatch?sslmode=disable
    depends_on:
      - db

  db:
    image: postgres:15
    environment:
      POSTGRES_DB: linkwatch
      POSTGRES_USER: linkwatch
      POSTGRES_PASSWORD: password
    volumes:
      - postgres_data:/var/lib/postgresql/data

volumes:
  postgres_data:
```

## Configuration

All configuration is done via environment variables with sensible defaults:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `DATABASE_URL` | SQLite in-memory | Database connection string |
| `CHECK_INTERVAL` | `15s` | How often to check all URLs |
| `MAX_CONCURRENCY` | `8` | Maximum concurrent checks |
| `HTTP_TIMEOUT` | `5s` | HTTP client timeout per request |
| `SHUTDOWN_GRACE` | `10s` | Graceful shutdown timeout |

## API Endpoints

### Create Target

Register a new URL for monitoring.

```bash
POST /v1/targets
Content-Type: application/json
Idempotency-Key: optional-key-123

{
  "url": "https://example.com"
}
```

**Response:**
- `201 Created` - New target created
- `200 OK` - Target already exists (idempotent)

```json
{
  "id": "t_1234567890",
  "url": "https://example.com",
  "created_at": "2025-08-17T12:34:56Z"
}
```

### List Targets

Get paginated list of registered targets.

```bash
GET /v1/targets?host=example.com&limit=10&page_token=abc123
```

**Response:**
```json
{
  "items": [
    {
      "id": "t_1234567890",
      "url": "https://example.com"
    }
  ],
  "next_page_token": "def456"
}
```

### Get Check Results

Retrieve recent check results for a target.

```bash
GET /v1/targets/t_1234567890/results?since=2025-08-17T12:00:00Z&limit=50
```

**Response:**
```json
{
  "items": [
    {
      "checked_at": "2025-08-17T12:00:01Z",
      "status_code": 200,
      "latency_ms": 123,
      "error": null
    },
    {
      "checked_at": "2025-08-17T11:59:46Z", 
      "status_code": null,
      "latency_ms": 5000,
      "error": "connection timeout"
    }
  ]
}
```

### Health Check

```bash
GET /healthz
```

Returns `200 OK` when the service is healthy.

## Testing

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests with race detection
go test -race ./...

# Run specific test package
go test ./internal/storage
```

## URL Canonicalization Rules

URLs are canonicalized to prevent duplicates:

1. Scheme and host are lowercased
2. Default ports are removed (`:80` for HTTP, `:443` for HTTPS)
3. Trailing slash is removed (except for root `/`)
4. Fragments (`#section`) are stripped
5. Query parameters are preserved

Examples:
- `HTTPS://Example.COM:443/path/` → `https://example.com/path`
- `http://example.com:80/` → `http://example.com`
- `https://example.com#section` → `https://example.com`

## Background Checking

The service runs background checks with the following behavior:

- **Interval**: Configurable via `CHECK_INTERVAL` (default 15s)
- **Concurrency**: Limited by `MAX_CONCURRENCY` (default 8)
- **Per-host serialization**: Only 1 request per host at a time
- **Retries**: Up to 2 additional attempts for 5xx/network errors
- **Backoff**: Exponential starting at 200ms (200ms, 400ms)
- **Redirects**: Follows up to 5 redirects
- **User-Agent**: `Linkwatch/1.0`

## Database Schema

### `targets` table
- `id` - Unique target identifier (primary key)
- `url` - Original URL as submitted
- `canonical_url` - Canonicalized URL (unique)
- `created_at` - Timestamp when target was created

### `check_results` table  
- `id` - Auto-increment primary key
- `target_id` - Foreign key to targets table
- `checked_at` - When the check was performed
- `status_code` - HTTP status code (null if request failed)
- `latency_ms` - Request latency in milliseconds
- `error` - Error message if request failed

### `idempotency_keys` table
- `key` - Idempotency key (primary key)  
- `target_id` - Associated target ID
- `created_at` - When the key was first used

## Architecture Decisions

See [DESIGN.md](DESIGN.md) for detailed architectural decisions and trade-offs.
