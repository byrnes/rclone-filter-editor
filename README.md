# rclone-filter-editor

A terminal-based interactive filter editor for rclone, built with Go and Bubble Tea.

**Note:** This tool is not perfect and you should expect bugs. It works for my personal use case, but your mileage may vary.

## Features

- Browse and navigate directory structures
- Create and edit rclone filter rules interactively
- Include/exclude files and directories with keyboard shortcuts
- Visual feedback showing which items are filtered
- Save filter rules to a file for use with rclone

## Installation

```bash
go build -o rclone-filter-editor
```

## Usage

```bash
# Start the editor with a directory
./rclone-filter-editor -p /path/to/directory

# Load an existing filter file
./rclone-filter-editor -p /path/to/directory -f filter.txt

# Specify number of concurrent checkers
./rclone-filter-editor -p /path/to/directory --checkers 8
```

## Controls

- **Arrow keys** / **j/k**: Navigate up/down
- **Enter**: Expand/collapse directories
- **Space**: Toggle include/exclude for item
- **i**: Invert selection
- **s**: Save filter to file
- **S**: Sort by last modified
- **h**: Show help
- **q**: Quit

## Filter Rules

The editor generates rclone-compatible filter rules:
- `+` prefix: Include rule
- `-` prefix: Exclude rule
- `**` wildcard: Matches any path depth
- Patterns ending with `/` match directories

## Requirements

- Go 1.16 or higher
- Works on Windows, macOS, and Linux

## License

GPL v3