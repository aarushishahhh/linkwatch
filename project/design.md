# Linkwatch take home assignment - Design

## Architecture Overview

- **HTTP API**: RESTful endpoints for URL registration and result retrieval
- **Background Checker**: Worker pool that periodically checks registered URLs
- **Storage Layer**: Abstracted database operations (SQLite support)
- **Configuration**: Environment-based configuration with sensible defaults

## Design Decisions

### 1. Database Choice & Schema Design

**Decision**: Support SQLite (development/testing)

**Rationale**:
- SQLite enables easy local development and testing without external dependencies
- Abstract storage layer allows swapping implementations

**Schema Design**:
- Separate `targets` and `check_results` tables for normalization
- `canonical_url` field enables deduplication while preserving original URLs
- Indexes on `(created_at, id)` for stable pagination
- `idempotency_keys` table with TTL for durable idempotency support

### 2. URL Canonicalization

**Decision**: Implement custom canonicalization rules rather than using library

**Rationale**:
- Simple rules sufficient for most use cases
- Avoids complex edge cases in third-party libraries
- Transparent behavior for users
- Easy to extend/modify rules as needed

**Rules Implemented**:
- Case normalization for scheme/host
- Default port removal
- Fragment stripping
- Path normalization (trailing slash handling)

**Edge Cases**:
- Preserves query parameters (business logic may depend on them)
- Root path `/` keeps trailing slash (HTTP standard)
- International domain names require additional consideration (future enhancement)

### 3. Concurrency Control

**Decision**: Two-level concurrency limiting (global + per-host)

**Global Limit (`MAX_CONCURRENCY`)**:
- Prevents overwhelming the service with too many simultaneous requests
- Controls resource usage (memory, file descriptors, network connections)
- Default of 8 balances throughput with resource consumption

**Per-Host Limit (1 per host)**:
- Respects target servers by avoiding thundering herd
- Prevents one service from being overwhelmed by multiple URL checks
- Uses host-specific semaphores with lazy initialization

**Implementation**:
- Worker pool pattern with global semaphore
- Per-host semaphores stored in concurrent-safe map
- Semaphore cleanup not implemented (acceptable for typical usage patterns)

### 4. Retry Logic & Error Handling

**Decision**: Retry 5xx and network errors, not 4xx errors

**Retry Strategy**:
- Maximum 3 attempts total (initial + 2 retries)
- Exponential backoff: 200ms, 400ms
- Only retry on server errors (5xx) and network failures

**Rationale**:
- 4xx errors indicate client problems (bad URL, forbidden) - retrying won't help
- 5xx errors may be transient (server overload, temporary failures)
- Network errors could be temporary connectivity issues
- Exponential backoff prevents overwhelming failing servers

**Error Recording**:
- Store both successful responses and failures
- Capture HTTP status codes when available
- Record error messages for network failures
- Always record latency (including failed attempts)

### 5. Pagination Strategy

**Decision**: Cursor-based pagination with composite cursor

**Cursor Design**: `{created_at}_{id}` format
- Stable ordering using `(created_at, id)` composite key
- `created_at` provides logical ordering
- `id` provides tie-breaking for same timestamp

**Advantages**:
- No skipped/duplicate results during concurrent writes
- Consistent performance regardless of offset
- Simple implementation without complex encoding

**Limitations**:
- Cursor is somewhat opaque to clients
- Backward pagination not supported (acceptable for this use case)

### 6. Idempotency Implementation

**Decision**: Durable idempotency keys stored in database

**Storage Strategy**:
- Separate `idempotency_keys` table
- Links idempotency key to created target ID
- Includes timestamp for potential cleanup

**Behavior**:
- Same key with same URL → return existing target
- Same key with different URL → return original target (key wins)
- No key provided → standard duplicate detection by canonical URL

**Cleanup**:
- Basic cleanup method provided but not automatically scheduled
- In production, would need periodic cleanup job
- Keys could have TTL (24-48 hours) to prevent unbounded growth

### 7. Background Check Scheduling

**Decision**: Simple timer-based scheduling with immediate execution

**Implementation**:
- Single goroutine with `time.Ticker`
- Checks all targets on each interval
- No persistent job queue or complex scheduling

**Trade-offs**:
- Simple and reliable
- May not scale to very large numbers of targets
- Could add jitter to prevent thundering herd
- More sophisticated scheduling (priority