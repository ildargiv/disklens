<p align="center">
  <img src="packaging/installer/icon-256.png" width="112" alt="DiskLens icon">
</p>

<h1 align="center">DiskLens by KopiTehnik</h1>

<p align="center">
  <b>Find out where your disk space went — and free it without breaking anything.</b><br>
  A fast, Windows-first disk usage analyzer with an interactive terminal UI, honest numbers,
  Recycle-Bin-only deletion and a built-in MCP server for AI agents.
</p>

<p align="center">
  <a href="https://github.com/ildargiv/disklens/actions/workflows/ci.yml"><img src="https://github.com/ildargiv/disklens/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/ildargiv/disklens/releases/latest"><img src="https://img.shields.io/github/v/release/ildargiv/disklens?label=download" alt="Latest release"></a>
  <img src="https://img.shields.io/badge/platform-Windows%2010%2F11%20%C2%B7%20x64%20%C2%B7%20ARM64-0078D6" alt="Platform">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green" alt="MIT license"></a>
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#quick-start">Quick start</a> ·
  <a href="#command-line">Command line</a> ·
  <a href="#ai-agents-mcp">AI agents (MCP)</a> ·
  <a href="#how-disklens-counts">How it counts</a> ·
  <a href="README.ru.md">Русская версия</a>
</p>

---

## Why DiskLens

When a disk is full, the real question is not *"what is big?"* — it is
*"what can I delete, how much will that actually free, and will anything break?"*

Most analyzers answer only the first half, and on Windows many of them get even
that wrong: junctions are counted twice, `WinSxS` looks twice as big as it is
because of hard links, OneDrive placeholders look like they take space they don't.

DiskLens is built around the second half:

- **It tells you what deleting would really free.** Sizes are *size on disk*
  (cluster-accurate, NTFS-compression-aware), hard links count once, and
  junctions/symlinks are never followed.
- **It never destroys anything by accident.** Deleting only ever moves items to
  the Recycle Bin, always behind a confirmation. If something is too big for the
  Recycle Bin, Windows asks before deleting it permanently instead of doing it silently.
- **It explains what a folder is.** Cleanup classification marks caches and build
  outputs as `safe`, things worth a look as `review_first`, and system data
  (WinSxS, Installer, Docker/WSL disks…) as `keep`, with a hint how to reclaim it the right way.
- **It is fast.** A parallel scanner enumerates directories in batches straight from
  NTFS — hundreds of thousands of files in about a second.
- **It talks to AI agents.** `disklens mcp` lets Claude, Cursor, OpenCode & co. measure,
  classify and (with confirmation) recycle — safely.

## Install

### Installer (recommended)

1. Open the **[latest release](https://github.com/ildargiv/disklens/releases/latest)**.
2. Download **`DiskLens-Setup-<version>.exe`** and run it.

The installer:

- needs **no administrator rights** (installs for the current user; choose "all users" if you prefer);
- contains both **x64 and ARM64** builds and picks the right one;
- optionally adds `disklens` to `PATH` and an **"Analyze with DiskLens"** item to the
  Explorer right-click menu for folders and drives;
- adds a Start menu shortcut and uninstalls cleanly (including `PATH` and the menu entry).

### Portable

Download `disklens-windows-amd64.exe` (or `-arm64.exe`) from the release, rename it
to `disklens.exe` and run it from anywhere — it is a single self-contained file.
Every release ships `SHA256SUMS.txt` to verify downloads.

### From source

```powershell
git clone https://github.com/ildargiv/disklens
cd disklens
go build ./cmd/disklens        # Go 1.25+
```

## Quick start

```powershell
disklens                 # browse the current folder
disklens C:\Users\me     # browse a folder
disklens D:\             # browse a whole drive
```

Or right-click any folder in Explorer → **Analyze with DiskLens**.

The scan shows live progress; press <kbd>Esc</kbd> to stop early and browse what was
found so far. Then navigate the tree:

| Key | Action |
|---|---|
| <kbd>↑</kbd><kbd>↓</kbd> / <kbd>j</kbd><kbd>k</kbd> | move |
| <kbd>Enter</kbd> / <kbd>→</kbd> / <kbd>l</kbd> | open folder |
| <kbd>Esc</kbd> / <kbd>←</kbd> / <kbd>h</kbd> | back to parent |
| <kbd>g</kbd> / <kbd>G</kbd>, <kbd>PgUp</kbd> / <kbd>PgDn</kbd> | top / bottom, page |
| <kbd>/</kbd> | filter by name |
| <kbd>s</kbd> <kbd>n</kbd> <kbd>c</kbd> | sort by size, name, file count |
| <kbd>a</kbd> | toggle size on disk ↔ apparent size |
| <kbd>d</kbd> / <kbd>Delete</kbd> | move to **Recycle Bin** (asks first, recoverable) |
| <kbd>x</kbd> | empty the Recycle Bin (asks first, permanent) |
| <kbd>r</kbd> | rescan the current folder |
| <kbd>o</kbd> | show the item in Explorer |
| <kbd>?</kbd> | help |
| <kbd>q</kbd> | quit |

The footer always shows how much is sitting in the Recycle Bin.

## Command line

When the output is not a terminal (or with `--no-tui`), DiskLens prints a tree instead. Example:

```text
> disklens --no-tui --depth 2 --top 3 C:\Users\me\Projects
  12.4 GiB [######......] ml-experiments\   (48 120 files, 2 311 dirs)
    9.8 GiB [#########...] checkpoints\   (40 files, 2 dirs)
    1.9 GiB [##..........] .venv\   (35 911 files, 2 004 dirs)
  402.0 MiB [............] + 12 more (12 209 files, 305 dirs)
   3.1 GiB [##..........] web-app\   (61 877 files, 7 402 dirs)
  ...
total 18.2 GiB on disk (17.6 GiB apparent)  |  140 233 files, 12 901 dirs  |  C:\Users\me\Projects  |  0.41s
```

| Flag | Meaning |
|---|---|
| `--depth N` | levels to show (default 1, `0` = everything) |
| `--top N` | only the N largest entries per level, the rest summed as "+ N more" |
| `--files` | list files too, not only folders |
| `--min-size SIZE` | hide entries smaller than `SIZE` (`500K`, `100M`, `1.5G`) |
| `--sort size\|name\|files` | order of entries |
| `--exclude GLOB` | skip matching names (repeatable); a pattern with `\` matches the full path |
| `--exclude-from FILE` | read exclude patterns from a file |
| `--apparent` | logical file sizes instead of size on disk |
| `--count-links` | count every hard link separately (default: once) |
| `--json` | machine-readable output (same field names as the MCP tools) |
| `--summarize` | totals only |
| `-o FILE` / `-f FILE` | export / import [ncdu-compatible JSON](https://dev.yorhel.nl/ncdu/jsonfmt) |
| `--strict` | exit code `2` if some folders could not be read |
| `--quiet` | no live progress on stderr |
| `--si`, `--ascii`, `--items=false`, `--workers N` | formatting and tuning |

Exit codes: `0` success · `1` error · `2` incomplete scan with `--strict`.

```powershell
# the 20 biggest files over 1 GB on drive D:, as JSON
disklens --json --files --depth 0 --min-size 1G D:\ > big.json

# a project tree without dependencies and VCS data
disklens --no-tui --exclude node_modules --exclude .git C:\src

# save a scan, browse it later (or in ncdu)
disklens -o scan.json C:\Users\me
disklens -f scan.json --depth 2
```

## AI agents (MCP)

`disklens mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) server
(stdio). Add it to your agent:

```json
{
  "mcpServers": {
    "disklens": { "command": "disklens", "args": ["mcp"] }
  }
}
```

(Use the full path, e.g. `C:\\Users\\me\\AppData\\Local\\Programs\\DiskLens\\disklens.exe`,
if `disklens` is not on `PATH`.)

| Tool | What it does |
|---|---|
| `scan` | scan (or refresh) a folder; returns totals |
| `top`, `children` | biggest / all direct children of a folder |
| `du` | totals for any file or folder |
| `largest_files` | biggest files anywhere below a folder, filterable by name glob, size and age |
| `classify` | cleanup candidates: `safe` / `review_first` / `keep`, each with a hint |
| `duplicates` | same-size file groups (candidates, not hash-verified) |
| `recycle_bin` | what is in the Recycle Bin |
| `delete_to_recycle` | move to the Recycle Bin — requires `confirm: true`, refuses drive roots, `Windows`, `.git`… |
| `empty_recycle_bin` | permanent — requires `confirm: true` and the user's explicit request |

Agents do not need to remember call order: every tool scans on demand and reports
`cache_age_seconds`. Full contract for agent authors: [AGENTS.md](AGENTS.md).

## How DiskLens counts

Being right on Windows is the whole point, so here is exactly what DiskLens does:

| Situation | DiskLens |
|---|---|
| Junctions, mount points, directory symlinks | never followed and never counted as files (reported as `skipped_links`) — no loops, no double counting |
| Hard links (e.g. `WinSxS` ↔ `System32`) | bytes counted **once** per scan; extra links are marked *hard link, counted once* (`--count-links` to disable) |
| NTFS-compressed and sparse files | real allocated size via `GetCompressedFileSizeW` |
| OneDrive / cloud placeholders | counted as files, with the space they actually occupy locally |
| `ACCESS_DENIED` folders | scan continues; folders are flagged and totals are marked incomplete |
| Paths longer than 260 characters, Unicode names | supported |
| Deleting | Recycle Bin only; Windows asks before anything would be destroyed permanently |

Directories are enumerated with `GetFileInformationByHandleEx(FileIdBothDirectoryInfo)`,
which returns names, sizes, allocation and file IDs for a whole batch of entries in one
system call — the same call gives speed *and* correct hard-link handling.

## Benchmarks

Measured on the author's Windows 11 machine (NVMe SSD, warm cache, apparent-size mode,
1 warm-up + 3 runs, min/mean/max) with v0.1; your numbers will differ.
Reproduce with `powershell -File scripts\bench.ps1`.

| Scenario | DiskLens | dua | gdu | dust |
|---|---|---|---|---|
| node_modules (35 k files) | **0.12 s** | 0.17 s | 0.30 s | 0.89 s |
| Desktop (photos + mixed, 72 GB) | **0.27 s** | 0.34 s | 0.53 s | 1.95 s |
| Comfy-Desktop (ML model files) | 0.23 s | **0.22 s** | 0.38 s | 1.88 s |
| AppData\Local (580 k tiny files, 160 GB) | **1.50 s** | 1.68 s | 2.76 s | 12.50 s |

DiskLens is on par with the fastest tool (dua) and ~2× faster than gdu. ncdu is not
listed because it has no native Windows build.

## Roadmap

DiskLens is heading from *"show me the bytes"* to *"free N GB for me, safely, with undo"*:

- [x] Parallel scanner with correct junction, hard-link and compression handling
- [x] Interactive TUI, Recycle-Bin-only deletion, Recycle Bin status
- [x] MCP server with on-demand scanning, cleanup classification for Windows
- [x] Installer with Explorer integration, built automatically by GitHub Actions
- [ ] **Plan & undo:** `disklens free 50GB` → a ready plan with risk per item; apply, then undo with one command
- [ ] **History:** snapshots and *"where did 18 GB go since last Tuesday?"*
- [ ] **MFT fast path** (admin): WizTree-class speed for whole drives
- [ ] Hash-verified duplicates, multi-select in the TUI, colors by file type

## Development

```powershell
go build ./...
go test ./...
```

Layout: `cmd/disklens` (CLI) · `internal/scanner` (parallel scanner) · `internal/tui` ·
`internal/mcpserver` · `internal/cleanup` (classifier) · `internal/dupes` ·
`internal/recycle` (Recycle Bin) · `internal/ncdujson` · `packaging/installer` (Inno Setup).

**Releasing:** push a tag like `v0.2.0`. GitHub Actions builds the portable binaries
(Windows x64/ARM64, Linux x64/ARM64), the installer, smoke-tests the installer
(install → run → uninstall) and publishes everything as a GitHub Release.
Every pull request builds the installer too — download it from the workflow run's artifacts.

## License

[MIT](LICENSE) © KopiTehnik
