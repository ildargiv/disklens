# disklens — Windows-first disk analyzer (аналог ncdu/du на Go)

**Идея:** современный переписанный аналог ncdu/du: быстрый многопоточный сканер,
TUI, designed *для Windows* с самого начала (junctions, корзина, длинные пути,
`ACCESS_DENIED`), ncdu-совместимый JSON экспорт, современный дизайн.

**Язык:** Go 1.22+, только stdlib на этапе ядра; TUI-библиотеки позже.
**Целевая ОС:** Windows primary, Linux secondary (код кроссплатформенный по конструкции).
**Лицензия:** MIT.

## Не-цели (чтобы не утонуть)

- Не GUI-приложение (TUI + опционально веб позже, как у gdu).
- Не клон ncdu 1:1 — совместимость только по JSON-формату экспорта.
- Не редактор файлов: удаление = в корзину, без rm.

## Этапы

### Этап 0 — Скелет ✅ (0.5 дня)
- go.mod, cmd/disklens, internal/scanner, README, LICENSE, PLAN.
- `go build` / `go test` зелёные.
- Критерий: рабочий бинарник, печатает размеры верхнего уровня папки.

### Этап 1 — Ядро: параллельный сканер (~1–2 недели)
- BFS-скан, worker pool, корректная агрегация размеров/счётчиков.
- Windows-специфика (часть уже в v0):
  - пропуск junction/reparse points (не зацикливаться, не двойной подсчёт);
  - пропуск directory symlinks; длинные пути `\\?\`;
  - `ACCESS_DENIED` → помеченная ошибка, скан продолжается;
  - размер "на диске" (GetCompressedFileSize) vs apparent — отдельный режим.
- Hard links: считать один раз (по volume+file-id, x/sys/windows).
- Тесты: go test + `-race`, табличные тесты размеров/счётчиков.
- Критерий: на 90k+ файлов сканер не уступает gdu/dust в hyperfine на HDD/SSD;
  отдельный прогон на медиа-архивах (много больших файлов — наш профиль).

### Этап 2 — CLI-режимы и экспорт ✅
- `-o` / `-f`: ncdu-совместимый JSON экспорт/импорт (format docs jsonfmt: asize/dsize/
  read_error; импортирует дампы ncdu, экспорт читается ncdu) — проверено циклом на
  реальном дереве (65 838 файлов, экспорт 3.9 MB, реимпорт идемпотентен).
- `--summarize`, `--apparent`, `--si`, флаги принимаются до и после пути, авто-ASCII при pipe.
- Реальный дисковый размер через GetCompressedFileSizeW (кластерный, с учётом сжатия NTFS).
- Тесты: round-trip export→import, канонический пример из документации ncdu, garbage-input.

### Этап 2.5 — MCP-сервер (`disklens mcp`) ✅

Все 7 инструментов: `scan` / `top` / `children` / `du` / `classify` / `delete_to_recycle` /
`empty_recycle_bin`. Корзина Windows через SHFileOperationW (FOF_ALLOWUNDO) + SHEmptyRecycleBinW.
classify: контекстно-маркерные правила (node_modules safe только рядом с package.json и т.п.),
протестирован на реальном дереве (1.1 GB safe-кандидатов у пользователя).
Safety: confirm-гейты, отказ на корнях дисков/system/.git (тест поймал слабую проверку — усилен
в двух слоях). tool-ошибки через error → SDK ставит isError (агент может самокорректироваться).

### Этап 3 — TUI ✅
bubbletea+lipgloss: live-прогресс (Progress callback + throttle), **esc во время скана =
досрочная остановка** (Cancel канал в сканере, сохраняется собранное, пометка "partial scan"),
навигация vim (j/k/enter/h/esc), сортировка s/n/c, фильтр `/`, **d = корзина с y-подтверждением**
(scanner.RemoveChild + Reaggregate для корректных итогов; Guard через recycle.Guard — тот же,
что у MCP). `--no-tui`/pipe/--summarize сохраняют plain-вывод.

### Этап 4 — Фичи-козыри (Windows-first) (~1 неделя)
- Размер корзины как отдельная строка + "очистить корзину".
- Cleanup projections: подсветка безопасных кандидатов — кэши (npm, pip, uv, NuGet,
  браузерные), Temp, node_modules рядом с package.json, build/target;
  "посмотреть сначала" — установщики, архивы фото.
- Репорты дубликатов-близнецов (по размеру+mtime, позже по hash) — полезно для фото-архивов.
- Цвет по типам файлов (медиа/архив/код).
- Критерий: то, чего нет ни у ncdu, ни у gdu на Windows.

### Этап 5 — Дизайн, документация, релиз (~1 неделя)
- README: скринкаст (VHS gif), hyperfine-таблица vs ncdu/du/gdu/dust/dua.
- GitHub Actions: cross-compile (windows/linux, amd64/arm64), winget/scoop/choco.
- Конфиг-файл (~/.disklens.yaml), completions.
- Критерий: публичный релиз v0.1.

## Технические решения

- Память: интерактивный режим держит всё дерево в памяти (узлы компактные);
  non-interactive `--summarize`/`--top` — bounded memory (агрегация на лету) — позже.
- Параллелизм: очередь BFS + пул воркеров; очередь без死deadlock'ов (pending-counter).
- Только stdlib на этапах 0–2; зависимость появляется только с TUI.

## бенчмарк-сценарии (наш контекст)

1. Фотоархив (немного папок, много файлов 5–30 MB) — профиль пользователя.
2. Деревья кэшей AppData (мелкая пыль, много tiny files).
3. Большой проект node_modules.
