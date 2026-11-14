# Design records

These documents record why significant changes were made and what their
authors intended at the time. They are kept for context and are not updated
as the code changes. The code and `CLAUDE.md` describe current behaviour. Each
record starts with a status note naming the PR that shipped it and any later
decisions that replaced parts of it.

| Date | Record | Summary | Status |
|------|--------|---------|--------|
| 2026-05-30 | [Huma pokemon v2/v3 scan](2026-05-30-huma-pokemon-v2-v3-scan-design.md) | Move the v2/v3 pokemon scan endpoints from gin to Huma for a self-documenting OpenAPI 3.1 spec. | Implemented (#368); partly superseded |
| 2026-05-30 | [Huma remaining APIs](2026-05-30-huma-remaining-apis-design.md) | Move the remaining `/api` endpoints to Huma with pointer-based response structs. | Implemented (#368) |
| 2026-07-04 | [Eviction lock contention](2026-07-04-eviction-lock-contention.md) | Entity-lock freezes traced to a tree-mutex convoy, fixed with a batched tree writer, shared scan snapshots and TTL jitter. | Implemented (#377) |
| 2026-07-14 | [Pokestop available API](2026-07-14-pokestop-available-api-design.md) | In-memory `GET /api/pokestop/available` replacing ReactMap's periodic SQL aggregate. | Implemented (#383); partly superseded |
| 2026-07-16 | [Fort scan map data](2026-07-16-fort-scan-map-data-design.md) | Serve ReactMap's fort markers from the in-memory fort scan, returning whole records. | Implemented (#385); partly superseded |
| 2026-07-16 | [Fort DNF filtering](2026-07-16-fort-dnf-filtering-design.md) | Push ReactMap's fort filters into the fort scan as DNF clauses. | Implemented (#385); partly superseded |
| 2026-07-17 | [Maintained fort availability](2026-07-17-maintained-fort-availability-design.md) | Max-expiry indexes replace per-request full-fort walks for availability. | Implemented (#385) |
| 2026-07-18 | [Decode performance findings](2026-07-18-decode-performance-findings.md) | Investigation record of proto-decode optimizations. | Dated record; wins shipped in #389 |
| 2026-07-29 | [Lure request-first](2026-07-29-lure-request-first-design.md) | Place lure pokemon from the request and drop the disk-encounter cache. | Implemented (#391) |
| 2026-08-16 | [Pokemon struct packing](2026-08-16-pokemon-struct-packing-design.md) | Shrink the cached `Pokemon` below Go's 512-byte GC threshold. | Implemented (#399); partly superseded |
| 2026-08-18 | [Fort id value type](2026-08-18-fortid-value-type-design.md) | Fort ids as a 17-byte `FortId` value type; optional username persistence. | Implemented (#399) |
| 2026-09-09 | [gRPC API](2026-09-09-grpc-api-design.md) | `GolbatApi` gRPC service mirroring the HTTP scans with protobuf results. | Implemented (#404) |
| 2026-10-28 | [Tappable scan API](2026-10-28-tappable-scan-api-design.md) | Tappable R-tree, DNF scan endpoint and per-entry cache TTL. | Implemented (#430) |
| 2026-10-28 | [Tappable webhook](2026-10-28-tappable-webhook-design.md) | `tappable` webhook for reward tappables. | Implemented (#431) |
