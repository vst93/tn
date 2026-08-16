# TN

A quiet, keyboard-first terminal note. TN stores every note as a normal Markdown file under `~/.tn`, with directories preserved as the note tree.

## Features

- Compact, bordered workbench layout with unified focus styling: active panels use the accent border, inactive panels and dialogs use muted borders
- Multi-level folder tree with mouse clicks, wheel scrolling, and touch-friendly targets
- Live Markdown editing with rendered preview, smart list continuation, and auto-indent
- Undo/redo with cursor preservation, plus auto-save after two seconds of idle typing
- One-key clipboard copy plus native terminal text selection mode
- In-note search with highlighted matches, global search across all notes, and jump-to-line
- Front-matter tags and tag filtering
- Pin favorite notes to the top of the tree, with reading-time estimates per note
- Quick-open any note by name or path with `Ctrl+O`, with a recent-notes list when the query is empty, plus per-note stats and scroll-based reading progress
- Back/forward note history for fast navigation between recently opened notes
- Focus mode for distraction-free writing and session persistence across restarts
- Export a note or a batch to Markdown or styled standalone HTML, to the clipboard, or to a file, plus batch delete and batch tag editing
- Command palette and a searchable, scrollable help overlay
- No database or lock-in: data is ordinary `.md` files
- Windows, Linux, macOS, and Termux support

## Install

Requires Go 1.23 or newer.

```sh
go install github.com/vst93/tn@latest
tn
```

From this repository:

```sh
go build -o tn .
./tn
```

Use a different note directory when needed:

```sh
tn -dir /path/to/notes
```

## Controls

| Action | Keyboard |
|---|---|
| Select note | `↑` / `↓`, `J` / `K` |
| Expand / collapse folder | `→` / `←`, `L` / `H`, `Enter` |
| Switch panel | `Tab` |
| Back / forward history | `Alt+←` / `Alt+→` |
| Quick open note | `Ctrl+O` |
| New note | `n`, `Ctrl+N` |
| New folder | `N`, `Ctrl+D` |
| Pin / unpin note | `*` |
| Rename | `F2`, `R` |
| Delete | `Delete`, `X` |
| Edit / preview | `Ctrl+E` |
| Save | `Ctrl+S` |
| Undo | `Ctrl+Z` |
| Redo | `Ctrl+Shift+Z`, `Ctrl+Y` |
| Copy current Markdown | `Ctrl+C`, `Ctrl+Y` |
| Copy current line | `Ctrl+L` (edit mode) |
| Select preview text | `Ctrl+G` |
| Search note | `Ctrl+F` |
| Search everywhere | `Ctrl+Shift+O` |
| Go to line | `Alt+G` |
| Edit tags | `Ctrl+Shift+T` |
| Filter by tag | `#` |
| Focus mode | `Ctrl+Shift+F` |
| Command palette | `Ctrl+Shift+P` |
| Toggle multi-select | `Space` |
| Select all | `Ctrl+A` |
| Clear selection | `Ctrl+Shift+A` |
| Export | `Ctrl+Shift+E` |
| Export as HTML | `Alt+H` |
| Refresh | `Ctrl+R` |
| Help | `?` |
| Quit | `Ctrl+Q` |
| Cancel / close | `Esc` |

While editing, standard terminal text editing keys and `Ctrl+V` paste are available. Press `Esc` to return to preview.

### Exporting

- `Ctrl+Shift+E` opens the export menu. From there you can copy the current rendered note, save the Markdown source, or export a standalone HTML page.
- `Alt+H` jumps straight to the HTML export path dialog for the current note.
- For a batch, select notes with `Space`, then `Ctrl+Shift+E` and choose Markdown or HTML before entering the destination directory.
- Exported HTML is a self-contained, styled document that embeds the note title, tags, and rendered Markdown. Raw HTML inside a note is sanitized before export.

### Copying text

- Press `Ctrl+C` (or `Ctrl+Y`) or choose **Copy** to copy the complete current Markdown source automatically. TN uses the native system clipboard when available and falls back to the OSC 52 terminal clipboard protocol.
- Press `Ctrl+G` or choose **Select** to temporarily release mouse control to the terminal. Drag across rendered preview text, then use the terminal's copy behavior. Many terminals automatically copy on selection; others use their normal copy shortcut. Press any key to restore TN mouse handling.
- Termux clipboard copy requires the Termux:API add-on and `termux-api` package when OSC 52 is unavailable.

Terminal applications cannot receive the exact range selected by the terminal, so automatic copy-on-release is controlled by the terminal rather than TN.

## Screenshots / Demo

TN is a TUI, so the rendered interface is only visible inside a terminal emulator. To capture a demo, run the app with `./tn` in a terminal that supports mouse events, then record the session with a tool such as `asciinema` or your terminal's screen recorder.

The default workbench shows a `Lists` tree panel on the left and a `Note` preview/edit panel on the right. The top bar shows the active note and dirty state, and the bottom bar shows contextual shortcuts, status messages, edit position, or search results depending on the current mode. The searchable help overlay (`?`) documents every shortcut by category.

Place captured screenshots or recordings in the repository and link them here to keep the README visual.

## Touch support

Terminal protocols expose mouse events, not a portable native touch API. TN uses broad row targets and a persistent action bar, so taps and scrolling work when the terminal translates touch into mouse input. This includes common Termux terminal setups. Native gestures such as pinch-to-zoom are terminal-specific and are not available through Bubble Tea.

## Data

The default note path is:

- Linux, macOS, Termux: `$HOME/.tn`
- Windows: `%USERPROFILE%\\.tn`

Only `.md` files are shown. Hidden files and non-Markdown files remain untouched.
