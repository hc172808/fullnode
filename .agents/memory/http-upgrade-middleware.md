---
name: HTTP upgrade middleware
description: Constraint for wrapping Go HTTP response writers around upgrade-based protocols
---

Any Go HTTP middleware that wraps `http.ResponseWriter` must delegate optional interfaces used by upgrade-based handlers, especially `http.Hijacker` for WebSocket connections.

**Why:** A wrapper that only forwards `Write` and `WriteHeader` causes Gorilla WebSocket handshakes to fail with HTTP 500 because the handler cannot take ownership of the underlying connection.

**How to apply:** When adding or changing logging, metrics, compression, or tracing middleware around WebSocket or streaming routes, preserve `Hijacker`, `Flusher`, `Pusher`, and `ReaderFrom` behavior where the underlying writer supports them.