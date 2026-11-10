# disklens

[![CI](https://github.com/ildargiv/disklens/actions/workflows/ci.yml/badge.svg)](https://github.com/ildargiv/disklens/actions/workflows/ci.yml)

Fast, modern disk usage analyzer — **built for Windows first**, written in Go.

An ncdu/du-style tool, but designed for how Windows actually behaves:
junctions and reparse points never cause loops or double counting, inaccessible
directories (`ACCESS_DENIED`) are reported instead of silently skipped, and
deletions only ever go to the Recycle Bin (recoverable, with confirmations in
both the TUI and the MCP tools).

It also speaks to AI agents: `disklens mcp` is an MCP server (9 tools) that
exposes scans, cleanup classification, duplicate candidates and
Recycle-Bin-safe deletion to agents like OpenCode, Claude or Cursor.

## Usage (current)

```
go build ./cmd/disklens
disklens C:\Users\you\Desktop            # scan and show top level
disklens --depth 2 --top 10 .            # two levels, 10 largest per level
disklens --workers 16 --ascii <dir>
```

## MCP server for AI agents

`disklens mcp` runs a stdio MCP server (official go-sdk): tools `scan`, `top`, `children`, `du`.
The agent decides what is safe to clean; disklens measures precisely and (later) deletes
only to the Recycle Bin. Tool contract: [AGENTS.md](AGENTS.md).

Connect it like any MCP server, e.g.:

```json
{
  "mcpServers": {
    "disklens": { "command": "C:\\tools\\disklens.exe", "args": ["mcp"] }
  }
}
```

## Benchmarks

Measured on a Windows 11 machine (ADATA NVMe), warm filesystem cache,
apparent-size mode (`--apparent` / `-a` / `-s` / `-A`), 3 runs after 1 warmup
(`scripts/bench.ps1`, results are min/mean/max of those runs). ncdu is not in
the table because it has no native Windows build — the common way to run it on
Windows is WSL, which is exactly the gap disklens fills.

| scenario | disklens | dua | gdu | dust |
|---|---|---|---|---|
| node_modules (35 k files) | **0.12s** | 0.17s | 0.30s | 0.89s |
| Desktop (photos + mixed, 72 GB) | **0.27s** | 0.34s | 0.53s | 1.95s |
| Comfy-Desktop (ML model files) | **0.23s** | 0.22s | 0.38s | 1.88s |
| AppData\Local (580 k tiny files, 160 GB) | **1.50s** | 1.68s | 2.76s | 12.50s |

disklens is fastest or on par with the fastest tool in every scenario; it is
consistently ~2x faster than gdu and 7–8x faster than dust on large trees.
Reproduce with `powershell -File scripts/bench.ps1`.

## Roadmap (short)

1. ✅ parallel BFS scanner core, Windows reparse-point handling
2. ✅ non-interactive CLI modes + ncdu-compatible JSON export/import
3. ✅ MCP server (9 tools): `scan`/`top`/`children`/`du`/`classify`/`duplicates`/
   `recycle_bin`/`delete_to_recycle`/`empty_recycle_bin`
4. ✅ TUI: live progress, esc early-stop, vim navigation, sorting, filter `/`,
   delete-to-Recycle-Bin with confirmation
5. ✅ Windows trump cards: Recycle Bin status line + `x` empty-bin flow (TUI and MCP),
   cleanup rules incl. ML/comfy model directories, size-grouped duplicate candidates
6. Release: benchmark table, screencast, GitHub Actions, winget/scoop

## Why

`ncdu` (2006, C→Zig) and its rewrite crowd (gdu, dust, dua, pdu) are all
unix-first. On Windows there is no good modern analyzer: TreeSize/SpaceSniffer
are old, single-threaded and closed-source. disklens fills that gap and adds an
AI-agent layer nobody else has.

MIT licensed.
