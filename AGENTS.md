# AGENTS.md — DiskLens by KopiTehnik

Guide for AI agents (coding agents and MCP-tool agents).

## For coding agents working in this repo

- Build: `go build ./...` · Test: `go test ./...` (no gcc needed; `-race` only in CI on Linux)
- Layout: `cmd/disklens` CLI · `internal/scanner` parallel BFS scanner · `internal/ncdujson`
  ncdu JSON export/import · `internal/mcpserver` MCP stdio server · `internal/tui` browser
  (bubbletea) · `internal/cleanup` classifier · `internal/dupes` duplicate candidates · `internal/recycle` Recycle Bin operations
- Packaging: `packaging/installer/disklens.iss` (Inno Setup, built and smoke-tested by
  `.github/workflows/release.yml` on every PR and tag); the exe icon/version info are
  generated in CI with go-winres (`*.syso` is git-ignored).
- Rules: scanner/ncdujson/cleanup/recycle are stdlib-only; external deps are go-sdk (MCP)
  and bubbletea+lipgloss (TUI); keep scanner allocation-light;
  Windows semantics are first-class (reparse points must never be descended; inaccessible
  directories are errors reported as data, never swallowed).
- Version lives in `internal/buildinfo.Version` (default `0.2.0-dev`); product name/vendor are `buildinfo.FullName`; release builds stamp it
  with `-ldflags "-X disklens/internal/buildinfo.Version=<tag>"` (the release workflow does this).
- Never introduce destructive behavior into scan paths. The only planned mutation tool is
  delete-to-Recycle-Bin, gated behind an explicit `confirm` argument.

## For agents consuming disklens as MCP tools

Server: `disklens mcp` (stdio).

| Tool | Input | Output |
|---|---|---|
| `scan` | `path`, optional `workers` | (re)scan + cache; totals: apparent/disk bytes, files, dirs, inaccessible_dirs, skipped_links |
| `top` | `path`, `limit` (default 20) | biggest children (dirs AND files) |
| `children` | `path`, `limit`, `sort` (size/name/files) | direct children |
| `du` | `path` | totals for one file or directory; instant from cache, else measured on the fly (not cached) |
| `largest_files` | `path`, optional `limit`, `min_size_bytes`, `name` (glob), `older_than_days` | biggest files anywhere in the subtree |
| `classify` | `path` | cleanup candidates: `safe` / `review_first` / `keep` (system data: reclaim another way), each with a `hint` |
| `duplicates` | `path`, optional `min_size_bytes` (default 5 MiB), `limit` | probable duplicate files: groups of 2+ files with identical apparent size, biggest waste first. **Candidates only** (not hash-verified) |
| `recycle_bin` | — | size and item count currently in the Windows Recycle Bin |
| `delete_to_recycle` | `path`, `confirm:true` | moves the item to the Windows **Recycle Bin** (recoverable). Refuses drive roots, system dirs, `.git` |
| `empty_recycle_bin` | `confirm:true` | PERMANENT. Call only after the user explicitly asked to empty it |

Contract:
- All sizes are **bytes**; `size_disk_bytes` is cluster-accurate on Windows (what deleting frees),
  `size_apparent_bytes` is the logical file size.
- `dirs`/`files`/`inaccessible_dirs` are subtree totals. A result with
  `inaccessible_dirs > 0` is incomplete — say so when reporting totals.
- Tree tools (`top`, `children`, `classify`, `largest_files`, `duplicates`) scan on demand when no
  cached scan covers the path; results carry `cache_age_seconds` (and `auto_scanned: true` when
  this call did the scan). Re-`scan` a root to refresh stale data.
- Junctions and directory symlinks are deliberately not scanned or counted (they would double-count);
  hard links contribute their bytes once per scan (`hard_link: true` marks the extra links).
- `delete_to_recycle` updates the cached trees, so follow-up answers reflect the freed space.
- `duplicates` groups files by identical apparent size only — results are candidates, not
  confirmed copies. Verify (mtime, hash, or by eye) before calling `delete_to_recycle` on them.
- Destructive contract: `delete_to_recycle` is the only deletion path and it is recoverable;
  never call `empty_recycle_bin` unless the user asked for it in so many words.
