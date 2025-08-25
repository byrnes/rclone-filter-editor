package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type FilterState int

const (
	FilterNone FilterState = iota
	FilterInclude
	FilterExclude
)

type loadingMsg struct {
	progress string
	dirs     int64
	files    int64
}

type treeReadyMsg struct {
	root *FileNode
}

type refreshMsg struct{}

type FileNode struct {
	Name     string
	Path     string
	IsDir    bool
	Size     int64
	Children []*FileNode
	Expanded bool
	Filter   FilterState
	Parent   *FileNode

	TotalSize  int64
	TotalFiles int
	Loading    bool
	mu         sync.RWMutex
}

type Model struct {
	root            *FileNode
	cursor          int
	visibleNodes    []*FileNode
	filterMap       map[string]FilterState
	filterFile      string
	showHelp        bool
	showSaveConfirm bool
	width           int
	height          int
	scrollOffset    int
	loading         bool
	loadProgress    string
	scannedDirs     int64
	scannedFiles    int64
	ctx             context.Context
	cancel          context.CancelFunc
	program         *tea.Program
	checkers        int
}

func main() {
	var filterFile string
	var basePath string
	var showHelp bool

	var checkers int
	flag.StringVar(&filterFile, "file", "", "Path to the rclone filter file")
	flag.StringVar(&filterFile, "f", "", "Path to the rclone filter file (shorthand)")
	flag.StringVar(&basePath, "path", "", "Base directory to browse (default: current directory)")
	flag.StringVar(&basePath, "p", "", "Base directory to browse (shorthand)")
	flag.IntVar(&checkers, "checkers", 4, "Number of concurrent directory scanning threads")
	flag.BoolVar(&showHelp, "help", false, "Show usage information")
	flag.BoolVar(&showHelp, "h", false, "Show usage information (shorthand)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS] [FILTER_FILE] [DIRECTORY]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Interactive terminal UI for editing rclone filter files.\n\n")
		fmt.Fprintf(os.Stderr, "Arguments:\n")
		fmt.Fprintf(os.Stderr, "  FILTER_FILE  Path to the rclone filter file (default: filter.txt)\n")
		fmt.Fprintf(os.Stderr, "  DIRECTORY    Directory to browse (default: current directory)\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s                           # Use filter.txt in current directory (4 threads)\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s myfilters.txt             # Use myfilters.txt in current directory\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --checkers 8 /path/dir    # Use 8 threads to scan /path/dir\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --file myfilters.txt      # Use --file flag\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --path /path/dir          # Browse /path/dir with default filter.txt\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -f filters.txt -p /path   # Use specific filter file and path\n", os.Args[0])
	}

	flag.Parse()

	if showHelp {
		flag.Usage()
		return
	}

	args := flag.Args()
	rootPath := "."

	// Use --path flag if provided, otherwise fall back to current logic
	if basePath != "" {
		rootPath = basePath
	}

	// Handle arguments: first arg can be filter file, second can be directory
	if filterFile == "" {
		if len(args) > 0 {
			filterFile = args[0]
			if len(args) > 1 && basePath == "" {
				// Only use positional directory arg if --path wasn't used
				rootPath = args[1]
			}
		} else {
			filterFile = "filter.txt"
		}
	} else {
		// If --file was used, first arg is directory (unless --path was also used)
		if len(args) > 0 && basePath == "" {
			rootPath = args[0]
		}
	}

	filterMap := loadFilterFile(filterFile)

	// Set the global root path for filter path calculations
	absRootPath, _ := filepath.Abs(rootPath)
	globalRootPath = absRootPath

	ctx, cancel := context.WithCancel(context.Background())

	if checkers < 1 {
		checkers = 4
	}

	m := Model{
		filterMap:    filterMap,
		filterFile:   filterFile,
		loading:      true,
		loadProgress: "Scanning directories...",
		ctx:          ctx,
		cancel:       cancel,
		checkers:     checkers,
	}

	// Initialize root node immediately for UI
	absPath, _ := filepath.Abs(rootPath)
	m.root = &FileNode{
		Name:     filepath.Base(absPath),
		Path:     absPath,
		IsDir:    true,
		Expanded: true,
		Loading:  true,
	}
	rootFilterPath := getFilterPath(absPath)
	m.root.Filter = getEffectiveFilter(rootFilterPath, filterMap)
	m.updateVisibleNodes()

	p := tea.NewProgram(&m, tea.WithAltScreen())
	m.program = p

	// Start async tree building after program is set
	go m.buildFileTreeAsync(rootPath)

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

}

func buildFileTree(rootPath string, filterMap map[string]FilterState) *FileNode {
	absPath, _ := filepath.Abs(rootPath)
	root := &FileNode{
		Name:     filepath.Base(absPath),
		Path:     absPath,
		IsDir:    true,
		Expanded: true,
	}

	rootFilterPath := getFilterPath(absPath)
	root.Filter = getEffectiveFilter(rootFilterPath, filterMap)

	buildTreeRecursive(root, filterMap)
	return root
}

func (m *Model) buildFileTreeAsync(rootPath string) {
	// Start background goroutine for breadth-first concurrent tree building
	go func() {
		m.buildTreeBreadthFirst(m.root, m.filterMap)
		// Send completion message
		if m.program != nil {
			m.program.Send(treeReadyMsg{root: m.root})
		}
	}()
}

// Breadth-first concurrent directory scanning
func (m *Model) buildTreeBreadthFirst(root *FileNode, filterMap map[string]FilterState) {
	// Use a queue for breadth-first traversal
	queue := []*FileNode{root}

	for len(queue) > 0 && m.ctx.Err() == nil {
		// Process current level
		currentLevel := queue
		queue = nil

		// Process directories at current level concurrently
		var wg sync.WaitGroup
		nextLevelChan := make(chan []*FileNode, len(currentLevel))
		semaphore := make(chan struct{}, m.checkers)

		for _, dir := range currentLevel {
			if !dir.IsDir {
				continue
			}

			wg.Add(1)
			go func(node *FileNode) {
				defer wg.Done()
				semaphore <- struct{}{}        // Acquire
				defer func() { <-semaphore }() // Release

				children := m.scanSingleDirectory(node, filterMap)
				nextLevelChan <- children
			}(dir)
		}

		// Wait for all directories in current level to complete
		go func() {
			wg.Wait()
			close(nextLevelChan)
		}()

		// Collect children for next level
		for children := range nextLevelChan {
			queue = append(queue, children...)
		}
	}
}

// Scan a single directory and return its child directories
func (m *Model) scanSingleDirectory(node *FileNode, filterMap map[string]FilterState) []*FileNode {
	select {
	case <-m.ctx.Done():
		return nil
	default:
	}

	entries, err := os.ReadDir(node.Path)
	if err != nil {
		node.mu.Lock()
		node.Loading = false
		node.mu.Unlock()
		return nil
	}

	// Update progress
	dirs := atomic.AddInt64(&m.scannedDirs, 1)
	if dirs%10 == 0 && m.program != nil {
		m.program.Send(loadingMsg{
			progress: "Scanning directories...",
			dirs:     dirs,
			files:    atomic.LoadInt64(&m.scannedFiles),
		})
	}

	var children []*FileNode
	var childDirectories []*FileNode

	for _, entry := range entries {
		childPath := filepath.Join(node.Path, entry.Name())
		child := &FileNode{
			Name:   entry.Name(),
			Path:   childPath,
			IsDir:  entry.IsDir(),
			Parent: node,
		}

		childFilterPath := getFilterPath(childPath)
		child.Filter = getEffectiveFilter(childFilterPath, filterMap)

		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				child.Size = info.Size()
			}
			files := atomic.AddInt64(&m.scannedFiles, 1)
			if m.program != nil && files%500 == 0 {
				m.program.Send(loadingMsg{
					progress: "Scanning directories...",
					dirs:     atomic.LoadInt64(&m.scannedDirs),
					files:    files,
				})
			}
		} else {
			child.Loading = true
			childDirectories = append(childDirectories, child)
		}

		children = append(children, child)
	}

	// Sort children
	sort.Slice(children, func(i, j int) bool {
		if children[i].IsDir != children[j].IsDir {
			return children[i].IsDir
		}
		return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
	})

	node.mu.Lock()
	node.Children = children
	node.Loading = false
	node.mu.Unlock()

	return childDirectories
}

func buildTreeRecursive(node *FileNode, filterMap map[string]FilterState) {
	// This function is kept for compatibility but not used in async version
	if !node.IsDir {
		return
	}

	entries, err := os.ReadDir(node.Path)
	if err != nil {
		return
	}

	for _, entry := range entries {
		childPath := filepath.Join(node.Path, entry.Name())
		child := &FileNode{
			Name:   entry.Name(),
			Path:   childPath,
			IsDir:  entry.IsDir(),
			Parent: node,
		}

		childFilterPath := getFilterPath(childPath)
		child.Filter = getEffectiveFilter(childFilterPath, filterMap)

		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				child.Size = info.Size()
			}
		}

		node.Children = append(node.Children, child)

		if child.IsDir {
			buildTreeRecursive(child, filterMap)
		}
	}

	sort.Slice(node.Children, func(i, j int) bool {
		if node.Children[i].IsDir != node.Children[j].IsDir {
			return node.Children[i].IsDir
		}
		return strings.ToLower(node.Children[i].Name) < strings.ToLower(node.Children[j].Name)
	})
}

func calculateStats(node *FileNode) (int64, int) {
	if !node.IsDir {
		return node.Size, 1
	}

	var totalSize int64
	var totalFiles int

	for _, child := range node.Children {
		size, files := calculateStats(child)
		totalSize += size
		totalFiles += files
	}

	node.TotalSize = totalSize
	node.TotalFiles = totalFiles
	return totalSize, totalFiles
}

func (m *Model) updateVisibleNodes() {
	m.visibleNodes = nil
	m.addVisibleNodesRecursive(m.root, 0)
}

func (m *Model) addVisibleNodesRecursive(node *FileNode, depth int) {
	m.visibleNodes = append(m.visibleNodes, node)

	if node.IsDir && node.Expanded {
		node.mu.RLock()
		children := node.Children
		node.mu.RUnlock()
		for _, child := range children {
			m.addVisibleNodesRecursive(child, depth+1)
		}
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return refreshMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case loadingMsg:
		m.loadProgress = msg.progress
		atomic.StoreInt64(&m.scannedDirs, msg.dirs)
		atomic.StoreInt64(&m.scannedFiles, msg.files)
		return m, nil

	case treeReadyMsg:
		m.loading = false
		m.root = msg.root
		calculateStats(m.root)
		m.updateVisibleNodes()
		return m, nil

	case refreshMsg:
		if m.loading {
			return m, tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
				return refreshMsg{}
			})
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}

		if m.showSaveConfirm {
			switch msg.String() {
			case "y", "Y":
				saveFilterFile(m.filterFile, m.filterMap)
				m.cancel()
				return m, tea.Quit
			case "n", "N":
				m.cancel()
				return m, tea.Quit
			case "c", "C", "escape":
				m.showSaveConfirm = false
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "q":
			m.showSaveConfirm = true
			return m, nil

		case "ctrl+c":
			m.cancel()
			return m, tea.Quit

		case "s":
			saveFilterFile(m.filterFile, m.filterMap)
			return m, nil

		case "?", "h":
			m.showHelp = true
			return m, nil

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.adjustScroll()
			}
			return m, nil

		case "down", "j":
			if m.cursor < len(m.visibleNodes)-1 {
				m.cursor++
				m.adjustScroll()
			}
			return m, nil

		case "left":
			if m.cursor < len(m.visibleNodes) {
				node := m.visibleNodes[m.cursor]
				if node.IsDir && node.Expanded {
					node.Expanded = false
					m.updateVisibleNodes()
					if m.cursor >= len(m.visibleNodes) {
						m.cursor = len(m.visibleNodes) - 1
					}
				} else if node.Parent != nil {
					for i, n := range m.visibleNodes {
						if n == node.Parent {
							m.cursor = i
							break
						}
					}
				}
			}
			return m, nil

		case "right", "enter":
			if m.cursor < len(m.visibleNodes) {
				node := m.visibleNodes[m.cursor]
				if node.IsDir && !node.Expanded {
					node.Expanded = true
					m.updateVisibleNodes()
				}
			}
			return m, nil

		case " ":
			if m.cursor < len(m.visibleNodes) {
				node := m.visibleNodes[m.cursor]
				node.Filter = (node.Filter + 1) % 3
				m.filterMap[getFilterPath(node.Path)] = node.Filter
				if node.Filter == FilterNone {
					delete(m.filterMap, getFilterPath(node.Path))
				}
			}
			return m, nil

		case "i":
			m.invertSelection()
			return m, nil

		case "r":
			m.resetFilters()
			return m, nil
		}
	}

	return m, nil
}

func (m *Model) adjustScroll() {
	visibleHeight := m.height - 4
	if visibleHeight <= 0 {
		visibleHeight = 20
	}

	if m.cursor < m.scrollOffset {
		m.scrollOffset = m.cursor
	} else if m.cursor >= m.scrollOffset+visibleHeight {
		m.scrollOffset = m.cursor - visibleHeight + 1
	}
}

func (m *Model) invertSelection() {
	for _, node := range m.visibleNodes {
		switch node.Filter {
		case FilterNone:
			continue
		case FilterInclude:
			node.Filter = FilterExclude
		case FilterExclude:
			node.Filter = FilterInclude
		}

		if node.Filter == FilterNone {
			delete(m.filterMap, getFilterPath(node.Path))
		} else {
			m.filterMap[getFilterPath(node.Path)] = node.Filter
		}
	}
}

func (m *Model) resetFilters() {
	for _, node := range m.visibleNodes {
		node.Filter = FilterNone
	}
	m.filterMap = make(map[string]FilterState)
}

func (m Model) View() string {
	if m.showHelp {
		return m.renderHelp()
	}

	if m.showSaveConfirm {
		return m.renderSaveConfirm()
	}

	if m.loading {
		return m.renderLoading()
	}

	var b strings.Builder

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	b.WriteString(headerStyle.Render("RClone Filter Editor"))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("Press ? for help, s to save, q to quit"))
	b.WriteString("\n\n")

	visibleHeight := m.height - 4
	if visibleHeight <= 0 {
		visibleHeight = 20
	}

	start := m.scrollOffset
	end := start + visibleHeight
	if end > len(m.visibleNodes) {
		end = len(m.visibleNodes)
	}

	for i := start; i < end; i++ {
		node := m.visibleNodes[i]
		depth := getNodeDepth(node)

		prefix := strings.Repeat("  ", depth)

		var icon string
		if node.IsDir {
			node.mu.RLock()
			isLoading := node.Loading
			node.mu.RUnlock()
			if isLoading {
				icon = "⟳ "
			} else if node.Expanded {
				icon = "▼ "
			} else {
				icon = "▶ "
			}
		} else {
			icon = "  "
		}

		var filterIcon string
		filterStyle := lipgloss.NewStyle()
		switch node.Filter {
		case FilterNone:
			filterIcon = "[ ]"
			filterStyle = filterStyle.Foreground(lipgloss.Color("8"))
		case FilterInclude:
			filterIcon = "[+]"
			filterStyle = filterStyle.Foreground(lipgloss.Color("10"))
		case FilterExclude:
			filterIcon = "[-]"
			filterStyle = filterStyle.Foreground(lipgloss.Color("9"))
		}

		nameStyle := lipgloss.NewStyle()
		if i == m.cursor {
			nameStyle = nameStyle.Background(lipgloss.Color("8")).Foreground(lipgloss.Color("15"))
		}

		line := fmt.Sprintf("%s%s%s %s", prefix, icon, filterStyle.Render(filterIcon), node.Name)

		var stats string
		if node.IsDir {
			stats = fmt.Sprintf(" (%s, %d files)", formatSize(node.TotalSize), node.TotalFiles)
		} else {
			stats = fmt.Sprintf(" (%s)", formatSize(node.Size))
		}

		if i == m.cursor {
			b.WriteString(nameStyle.Render(line + stats))
		} else {
			b.WriteString(line)
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(stats))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func (m Model) renderHelp() string {
	helpStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("12")).
		Padding(1, 2)

	help := `Keyboard Shortcuts:

Navigation:
  ↑/↓ or j/k  Navigate up/down
  ←/→         Collapse/expand directories
  Enter       Expand directory

Filters:
  Space       Toggle filter (none → include → exclude)
  i           Invert selection
  r           Reset all filters

Other:
  ? or h      Show this help
  s           Save filters to file
  q           Quit (asks to save)
  Ctrl+C      Quit immediately without saving

Press any key to close this help`

	return helpStyle.Render(help)
}

func (m Model) renderSaveConfirm() string {
	confirmStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("11")).
		Padding(1, 2).
		Width(50).
		Align(lipgloss.Center)

	confirm := fmt.Sprintf(`Save changes to %s before quitting?

[Y] Yes, save and quit
[N] No, quit without saving  
[C] Cancel and continue editing`, m.filterFile)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, confirmStyle.Render(confirm))
}

func (m Model) renderLoading() string {
	loadingStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("12")).
		Padding(2, 4).
		Align(lipgloss.Center)

	spinner := "⟳"
	switch (time.Now().UnixNano() / 200000000) % 4 { // Slower spinner rotation
	case 0:
		spinner = "▐"
	case 1:
		spinner = "▌"
	case 2:
		spinner = "▀"
	case 3:
		spinner = "▄"
	}

	dirs := atomic.LoadInt64(&m.scannedDirs)
	files := atomic.LoadInt64(&m.scannedFiles)

	loadingText := fmt.Sprintf(`%s Loading Directory Tree...

%s
Directories: %d
Files: %d
Threads: %d

Press Ctrl+C to cancel`,
		spinner, m.loadProgress, dirs, files, m.checkers)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, loadingStyle.Render(loadingText))
}

func getNodeDepth(node *FileNode) int {
	depth := 0
	for node.Parent != nil {
		depth++
		node = node.Parent
	}
	return depth
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

var globalRootPath string

func getFilterPath(path string) string {
	// Use the root path that was provided to the program
	absPath, _ := filepath.Abs(path)

	// Use global root path if set, otherwise fall back to current working directory
	rootPath := globalRootPath
	if rootPath == "" {
		wd, _ := os.Getwd()
		rootPath = wd
	}

	rel, err := filepath.Rel(rootPath, absPath)
	if err != nil {
		return filepath.ToSlash(filepath.Base(path))
	}
	return "/" + filepath.ToSlash(rel)
}

// matchesRclonePattern checks if a path matches an rclone filter pattern
func matchesRclonePattern(pattern, path string) bool {
	// Handle empty or invalid patterns
	if pattern == "" {
		return false
	}

	// Remove leading '/' from pattern if present for matching
	cleanPattern := strings.TrimPrefix(pattern, "/")
	cleanPath := strings.TrimPrefix(path, "/")

	// Convert rclone pattern to regex
	regex := rclonePatternToRegex(cleanPattern)

	// Compile and match regex
	re, err := regexp.Compile("^" + regex + "$")
	if err != nil {
		// Fallback to exact string match if regex compilation fails
		return cleanPattern == cleanPath
	}

	return re.MatchString(cleanPath)
}

// rclonePatternToRegex converts an rclone filter pattern to a regex pattern
func rclonePatternToRegex(pattern string) string {
	var result strings.Builder

	i := 0
	for i < len(pattern) {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				// ** matches everything including directory separators
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					// **/ should match zero or more directories
					result.WriteString("(?:.*/)?")
					i += 3 // Skip the '**/'
				} else if i+2 == len(pattern) {
					// ** at end matches everything
					result.WriteString(".*")
					i += 2 // Skip both '*' characters
				} else {
					result.WriteString(".*")
					i += 2 // Skip both '*' characters
				}
			} else {
				// * matches any sequence except directory separators
				result.WriteString("[^/]*")
				i++
			}

		case '?':
			// ? matches any single character except directory separator
			result.WriteString("[^/]")
			i++
		case '[':
			// Character class - find the closing ]
			j := i + 1
			for j < len(pattern) && pattern[j] != ']' {
				j++
			}
			if j < len(pattern) {
				// Found closing ], copy the character class
				result.WriteString(pattern[i : j+1])
				i = j + 1
			} else {
				// No closing ], treat as literal [
				result.WriteString("\\[")
				i++
			}
		case '{':
			// Pattern alternatives like {*.txt,*.md}
			j := i + 1
			braceLevel := 1
			for j < len(pattern) && braceLevel > 0 {
				if pattern[j] == '{' {
					braceLevel++
				} else if pattern[j] == '}' {
					braceLevel--
				}
				j++
			}
			if braceLevel == 0 {
				// Found matching closing brace
				alternatives := pattern[i+1 : j-1]
				parts := strings.Split(alternatives, ",")
				result.WriteString("(?:")
				for idx, part := range parts {
					if idx > 0 {
						result.WriteString("|")
					}
					result.WriteString(rclonePatternToRegex(part))
				}
				result.WriteString(")")
				i = j
			} else {
				// No matching closing brace, treat as literal {
				result.WriteString("\\{")
				i++
			}
		case '.', '^', '$', '+', '(', ')', '|', '\\':
			// Escape regex special characters
			result.WriteString("\\")
			result.WriteByte(pattern[i])
			i++
		default:
			result.WriteByte(pattern[i])
			i++
		}
	}

	return result.String()
}

// getEffectiveFilter determines the effective filter state for a path
// considering both exact matches and pattern matches
func getEffectiveFilter(path string, filterMap map[string]FilterState) FilterState {
	// First check for exact match
	if state, ok := filterMap[path]; ok {
		return state
	}

	// Then check for pattern matches
	var matchedState FilterState = FilterNone
	longestMatch := ""

	for pattern, state := range filterMap {
		if matchesRclonePattern(pattern, path) {
			// Prefer more specific (longer) patterns
			if len(pattern) > len(longestMatch) {
				matchedState = state
				longestMatch = pattern
			}
		}
	}

	return matchedState
}

func loadFilterFile(filename string) map[string]FilterState {
	filterMap := make(map[string]FilterState)

	file, err := os.Open(filename)
	if err != nil {
		return filterMap
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "+ ") {
			path := strings.TrimPrefix(line, "+ ")
			filterMap[path] = FilterInclude
		} else if strings.HasPrefix(line, "- ") {
			path := strings.TrimPrefix(line, "- ")
			filterMap[path] = FilterExclude
		}
	}

	return filterMap
}

func saveFilterFile(filename string, filterMap map[string]FilterState) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	var includes []string
	var excludes []string

	for path, state := range filterMap {
		switch state {
		case FilterInclude:
			includes = append(includes, path)
		case FilterExclude:
			excludes = append(excludes, path)
		}
	}

	sort.Strings(includes)
	sort.Strings(excludes)

	writer := bufio.NewWriter(file)

	for _, path := range includes {
		fmt.Fprintf(writer, "+ %s\n", path)
	}

	for _, path := range excludes {
		fmt.Fprintf(writer, "- %s\n", path)
	}

	return writer.Flush()
}
