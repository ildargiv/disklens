# disklens

Fast, modern disk usage analyzer — **built for Windows first**, written in Go.

An ncdu/du-style tool, but designed for how Windows actually behaves:
junctions and reparse points never cause loops or double counting, inaccessible
directories (`ACCESS_DENIED`) are reported instead of silently skipped, and
(roadmap) deletions only ever go to the Recycle Bin.

> Status: work in progress. Current state and full roadmap: [PLAN.md](PLAN.md).

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

## Roadmap (short)

1. ✅ parallel BFS scanner core, Windows reparse-point handling
2. ✅ non-interactive CLI modes + ncdu-compatible JSON export/import
3. ✅ MCP server: `scan`/`top`/`children`/`du`/`classify`/`delete_to_recycle`/`empty_recycle_bin`
4. ✅ TUI: live progress, esc early-stop, vim navigation, sorting, filter `/`,
   delete-to-Recycle-Bin with confirmation
5. Windows trump cards: Recycle Bin size + cleanup, cleanup projections
   (caches, Temp, node_modules), duplicate candidates for photo archives

## Why

`ncdu` (2006, C→Zig) and its rewrite crowd (gdu, dust, dua, pdu) are all
unix-first. On Windows there is no good modern analyzer: TreeSize/SpaceSniffer
are old, single-threaded and closed-source. disklens fills that gap and adds an
AI-agent layer nobody else has.

MIT licensed.
