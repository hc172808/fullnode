---
name: Go archive checksum verification
description: Reliable checksum source and mismatch diagnostics for the GYDS installer.
---

Verify a Go archive against the SHA256 value for its exact version and filename
in Go's official release JSON metadata. Validate the checksum format and fail
closed if metadata is unavailable or the downloaded bytes do not match. On
mismatch, report expected and actual digests plus the downloaded byte count.

**Why:** the Go `.sha256` URL returned HTML in testing, while the official JSON
metadata matched a fresh archive download. A server-side mismatch can also
indicate that a proxy or download filter altered the archive.

**How to apply:** preserve exact release-metadata matching, fail-closed
verification, and useful mismatch diagnostics in future installer changes;
never skip checksum verification.
