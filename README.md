# Vnote

A quiet, keyboard-first terminal notebook. Vnote stores every note as a normal Markdown file under `~/.vnote`, with directories preserved as the notebook tree.

## Features

- Compact, bordered workbench layout with unified focus styling: active panels use the accent border, inactive panels and dialogs use muted borders
- Multi-level folder tree with mouse clicks, wheel scrolling, and touch-friendly targets
- Live Markdown editing with rendered preview, smart list continuation, and auto-indent
- Undo/redo with cursor preservation, plus auto-save after two seconds of idle typing
- One-key clipboard copy plus native terminal text selection mode
- In-note search with highlighted matches, global search across all notes, and jump-to-line
- Front-matter tags, tag filtering, and note templates for blank, daily, meeting, and book notes
- Pin favorite notes to the top of the tree, with reading-time estimates per note
- Quick-open any note by name or path with `Ctrl+O`, plus per-note stats and scroll-based reading progress
- Back/forward note history for fast navigation between recently opened notes
- Focus mode for distraction-free writing and session persistence across restarts
- Export a note or a batch to the clipboard or a file, plus batch delete and batch tag editing
- Command palette and a searchable, scrollable help overlay
- No database or lock-in: data is ordinary `.md` files
- Windows, Linux, macOS, and Termux support

## Install

Requires Go 1.23 or newer.

```sh
go install github.com/vst93/vnote@latest
vnote
```

From this repository:

```sh
go build -o vnote .
./vnote
```

Use a different notebook directory when needed:

```sh
vnote -dir /path/to/notes
```

## Controls

| Action | Keyboard |
|---|---|
| Select note | `↑` / `↓`, `J` / `K` |
| Expand / collapse folder | `→` / `←`, `L` / `H`, `Enter` |
| Switch panel | `Tab` |
| Back / forward history | `Alt+←` / `Alt+→` |
| Quick open note | `Ctrl+O` |
| New note (with template) | `Ctrl+N` |
| New folder | `Ctrl+D` |
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
| Refresh | `Ctrl+R` |
| Help | `?` |
| Quit | `Ctrl+Q` |
| Cancel / close | `Esc` |

While editing, standard terminal text editing keys and `Ctrl+V` paste are available. Press `Esc` to return to preview.

### Copying text

- Press `Ctrl+C` (or `Ctrl+Y`) or choose **Copy** to copy the complete current Markdown source automatically. Vnote uses the native system clipboard when available and falls back to the OSC 52 terminal clipboard protocol.
- Press `Ctrl+G` or choose **Select** to temporarily release mouse control to the terminal. Drag across rendered preview text, then use the terminal's copy behavior. Many terminals automatically copy on selection; others use their normal copy shortcut. Press any key to restore Vnote mouse handling.
- Termux clipboard copy requires the Termux:API add-on and `termux-api` package when OSC 52 is unavailable.

Terminal applications cannot receive the exact range selected by the terminal, so automatic copy-on-release is controlled by the terminal rather than Vnote.

## Screenshots / Demo

Vnote is a TUI, so the rendered interface is only visible inside a terminal emulator. To capture a demo, run the app with `./vnote` in a terminal that supports mouse events, then record the session with a tool such as `asciinema` or your terminal's screen recorder.

The default workbench shows a `Lists` tree panel on the left and a `Note` preview/edit panel on the right. The top bar shows the active note and dirty state, and the bottom bar shows contextual shortcuts, status messages, edit position, or search results depending on the current mode. The searchable help overlay (`?`) documents every shortcut by category.

Place captured screenshots or recordings in the repository and link them here to keep the README visual.

## Touch support

Terminal protocols expose mouse events, not a portable native touch API. Vnote uses broad row targets and a persistent action bar, so taps and scrolling work when the terminal translates touch into mouse input. This includes common Termux terminal setups. Native gestures such as pinch-to-zoom are terminal-specific and are not available through Bubble Tea.

## Data

The default notebook path is:

- Linux, macOS, Termux: `$HOME/.vnote`
- Windows: `%USERPROFILE%\\.vnote`

Only `.md` files are shown. Hidden files and non-Markdown files remain untouched.
