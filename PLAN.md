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

### Этап 1 — Ядро: параллельный сканер ✅
- BFS-скан, worker pool, корректная агрегация размеров/счётчиков.
- Windows-специфика (часть уже в v0):
  - пропуск junction/reparse points (не зацикливаться, не двойной подсчёт);
  - пропуск directory symlinks; длинные пути `\\?\`;
  - `ACCESS_DENIED` → помеченная ошибка, скан продолжается;
  - размер "на диске" (GetCompressedFileSize) vs apparent — отдельный режим.
- Hard links: считать один раз (по volume+file-id, x/sys/windows) — 🚧 отложено (наш профиль
  фото/кэшей почти не содержит hard links; перенесено в этап 5).
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
`empty_recycle_bin` (на этапе 4 добавились `recycle_bin` и `duplicates` — всего 9). Корзина Windows через SHFileOperationW (FOF_ALLOWUNDO) + SHEmptyRecycleBinW.
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

### Этап 4 — Фичи-козыри (Windows-first) ✅ (кроме цветовой маркировки типов)
- **Корзина как отдельная строка:** `recycle.Query` (SHQueryRecycleBinW; пустая корзина =
  S_FALSE/ERROR_NO_MORE_FILES, а не ошибка) → статус-строка в TUI (`Recycle Bin: X GiB, N items`),
  **x = очистить корзину** с явным PERMANENT-предупреждением и y-подтверждением; MCP-инструмент
  `recycle_bin`. После удаления/очистки TUI мгновенно переопрашивает корзину.
- **classify-правила под ML-профиль** ( motivated реальным сканом: Comfy-Desktop = 108 GB):
  новый контекстный признак `pathAny` — `models`/`checkpoints`/`custom_nodes`/`input`
  классифицируются только рядом с comfy/ollama/huggingface/stable-diffusion/... (review_first,
  не safe); игровая папка `MyGame\models` не ложно срабатывает (тест).
- **Дубликаты-близнецы:** `internal/dupes` — группы файлов с одинаковым apparent size,
  сортировка по бесполезным GB, пропуск `.git`; `FileEntry.MTime` собран наготове для
  следующего шага (hash-верификация позже). MCP-инструмент `duplicates` (кандидаты по размеру,
  не подтверждённые копии).
- Цвет по типам файлов — 🚧 отложено до этапа 5.

### Этап 5 — Дизайн, документация, релиз 🚧
- ✅ Бенчмарк: `scripts/bench.ps1` (gdu/dust/dua; ncdu не имеет нативной Windows-сборки) —
  disklens быстрейший или паритетный во всех 4 сценариях, таблица в README.
- ✅ CI: GitHub Actions — test (ubuntu+windows, `-race` на Linux) + release по тегу
  (cross-compile windows/linux × amd64/arm64, version-стеймпинг через `internal/buildinfo`).
- ✅ Версия: `disklens version` / `--version`, `-ldflags -X buildinfo.Version`.
- ✅ Packaging-заготовки: `packaging/winget/` (portable-манифест) и `packaging/scoop/`
  (с autoupdate) — PR в winget-pkgs после первого релиза.
- 🚧 Скринкаст (VHS gif) в README.
- Отложено до v0.2: конфиг-файл, shell completions, hard-link дедупликация,
  hash-верификация дубликатов, цветовая маркировка типов файлов в TUI.
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
