# AGENTS.md — disklens

Guide for AI agents (coding agents and MCP-tool agents).

## For coding agents working in this repo

- Build: `go build ./...` · Test: `go test ./...` (no gcc needed; `-race` only in CI on Linux)
- Layout: `cmd/disklens` CLI · `internal/scanner` parallel BFS scanner · `internal/ncdujson`
  ncdu JSON export/import · `internal/mcpserver` MCP stdio server
- Rules: stdlib-first (the only external dep is go-sdk for MCP); keep scanner allocation-light;
  Windows semantics are first-class (reparse points must never be descended; inaccessible
  directories are errors reported as data, never swallowed).
- Never introduce destructive behavior into scan paths. The only planned mutation tool is
  delete-to-Recycle-Bin, gated behind an explicit `confirm` argument.

## For agents consuming disklens as MCP tools

Server: `disklens mcp` (stdio).

| Tool | Input | Output |
|---|---|---|
| `scan` | `path`, optional `workers` | totals: apparent/disk bytes, files, dirs, inaccessible_dirs |
| `top` | `path`, `limit` (default 20) | biggest children (dirs AND files) of a cached scan |
| `children` | `path`, `limit`, `sort` (size/name/files) | direct children of any cached path |
| `du` | `path` | totals for one path; instant from cache, else scanned on the fly |
| `classify` | `path` | cleanup candidates from a cached scan: `safe` vs `review_first`, with `hint` how each is recreated |
| `delete_to_recycle` | `path`, `confirm:true` | moves the item to the Windows **Recycle Bin** (recoverable). Refuses drive roots, system dirs, `.git` |
| `empty_recycle_bin` | `confirm:true` | PERMANENT. Call only after the user explicitly asked to empty it |

Contract:
- All sizes are **bytes**; `size_disk_bytes` is cluster-accurate on Windows (what deleting frees),
  `size_apparent_bytes` is the logical file size.
- `dirs`/`files`/`inaccessible_dirs` are subtree totals. A result with
  `inaccessible_dirs > 0` is incomplete — say so when reporting totals.
- `top`/`children` require a prior `scan()` covering the path (or an ancestor); they return a
  tool error explaining this — call `scan` and retry.
- Junctions and directory symlinks are deliberately not scanned or counted (they would double-count).
- Scans are cached server-side; re-`scan` a root to refresh. Prefer `du` for one-off questions.
- Destructive contract: `delete_to_recycle` is the only deletion path and it is recoverable;
  never call `empty_recycle_bin` unless the user asked for it in so many words.
