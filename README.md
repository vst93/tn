# Vnote

A quiet, keyboard-first terminal notebook. Vnote stores every note as a normal Markdown file under `~/.vnote`, with directories preserved as the notebook tree.

## Features

- Compact, bordered workbench layout inspired by high-density terminal tools
- Multi-level folder tree
- Markdown editing and rendered preview
- Mouse clicks and wheel scrolling
- One-key clipboard copy plus native terminal text selection mode
- Touch-friendly top action bar in terminals that translate touch to mouse events
- Create, rename, delete, refresh, and save without leaving the TUI
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

| Action | Keyboard | Mouse / touch |
|---|---|---|
| Select note | `↑` / `↓`, `J` / `K` | Tap/click row |
| Expand folder | `→`, `L`, `Enter` | Tap/click selected folder |
| Collapse folder | `←`, `H` | Tap/click selected folder |
| Switch panel | `Tab` | Tap/click either panel |
| Edit / preview | `Ctrl+E` or `E` | Edit action |
| Save | `Ctrl+S` or `S` | Save action |
| Copy current Markdown | `Ctrl+C`, `Ctrl+Y`, or `Y` | Copy action |
| Select preview text | `Ctrl+G` or `G` | Select action, then drag |
| New note | `Ctrl+N` or `N` | New note action |
| New folder | `Ctrl+D` or `D` | Folder action |
| Rename | `F2` or `R` | Rename action |
| Delete | `Delete` or `X` | Delete action |
| Refresh | `Ctrl+R` | — |
| Help | `?` | ? action |
| Quit | `Ctrl+Q` or `Q` | Quit action |

While editing, standard terminal text editing keys and `Ctrl+V` paste are available. Press `Esc` to return to preview.

### Copying text

- Press `Ctrl+C` (or `Ctrl+Y`) or choose **Copy** to copy the complete current Markdown source automatically. Vnote uses the native system clipboard when available and falls back to the OSC 52 terminal clipboard protocol.
- Press `Ctrl+G` or choose **Select** to temporarily release mouse control to the terminal. Drag across rendered preview text, then use the terminal's copy behavior. Many terminals automatically copy on selection; others use their normal copy shortcut. Press any key to restore Vnote mouse handling.
- Termux clipboard copy requires the Termux:API add-on and `termux-api` package when OSC 52 is unavailable.

Terminal applications cannot receive the exact range selected by the terminal, so automatic copy-on-release is controlled by the terminal rather than Vnote.

## Touch support

Terminal protocols expose mouse events, not a portable native touch API. Vnote uses broad row targets and a persistent action bar, so taps and scrolling work when the terminal translates touch into mouse input. This includes common Termux terminal setups. Native gestures such as pinch-to-zoom are terminal-specific and are not available through Bubble Tea.

## Data

The default notebook path is:

- Linux, macOS, Termux: `$HOME/.vnote`
- Windows: `%USERPROFILE%\\.vnote`

Only `.md` files are shown. Hidden files and non-Markdown files remain untouched.
