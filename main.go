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

type SortMode int

const (
	SortByName SortMode = iota
	SortBySize
	SortByFileCount
	SortByLastModified
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

type refreshDirMsg struct{}

type FileNode struct {
	Name     string
	Path     string
	IsDir    bool
	Size     int64
	ModTime  time.Time
	Children []*FileNode
	Expanded bool
	Filter   FilterState
	Parent   *FileNode

	TotalSize  int64
	TotalFiles int
	Loading    bool
	mu         sync.RWMutex
}

type FilterRule struct {
	Pattern string
	State   FilterState
}

type Model struct {
	root            *FileNode
	cursor          int
	visibleNodes    []*FileNode
	filterRules     []FilterRule
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
	sortMode        SortMode
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
		fmt.Fprintf(os.Stderr, "  %s test/folder_a             # Browse test/folder_a with default filter.txt\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s myfilters.txt             # Use myfilters.txt in current directory\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s myfilters.txt test/folder_a # Use myfilters.txt to browse test/folder_a\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --checkers 8 -p test/folder_a # Use 8 threads to scan test/folder_a\n", os.Args[0])
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
			// Check if the first argument is a directory - if so, use it as the path
			// and use default filter file
			if stat, err := os.Stat(args[0]); err == nil && stat.IsDir() && basePath == "" {
				// Single argument is a directory, use default filter file
				rootPath = args[0]
				filterFile = "filter.txt"
			} else {
				// First argument is a filter file
				filterFile = args[0]
				if len(args) > 1 && basePath == "" {
					// Only use positional directory arg if --path wasn't used
					rootPath = args[1]
				}
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

	filterRules, filterMap := loadFilterFile(filterFile)

	// Set the global root path for filter path calculations
	absRootPath, _ := filepath.Abs(rootPath)
	globalRootPath = absRootPath

	ctx, cancel := context.WithCancel(context.Background())

	if checkers < 1 {
		checkers = 4
	}

	m := Model{
		filterRules:  filterRules,
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
	m.root.Filter = getEffectiveFilter(rootFilterPath, m.filterRules)
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

func buildFileTree(rootPath string, filterRules []FilterRule) *FileNode {
	absPath, _ := filepath.Abs(rootPath)
	root := &FileNode{
		Name:     filepath.Base(absPath),
		Path:     absPath,
		IsDir:    true,
		Expanded: true,
	}

	rootFilterPath := getFilterPath(absPath)
	root.Filter = getEffectiveFilter(rootFilterPath, filterRules)

	buildTreeRecursive(root, filterRules)
	return root
}

func (m *Model) buildFileTreeAsync(rootPath string) {
	// Start background goroutine for breadth-first concurrent tree building
	go func() {
		m.buildTreeBreadthFirst(m.root, m.filterRules)
		// Send completion message
		if m.program != nil {
			m.program.Send(treeReadyMsg{root: m.root})
		}
	}()
}

func (m *Model) refreshDirectory() {
	if m.root == nil {
		return
	}

	// Cancel any existing operations
	m.cancel()

	// Create new context for refresh operation
	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	m.cancel = cancel

	// Reset loading state
	m.loading = true
	m.loadProgress = "Refreshing directory tree..."
	atomic.StoreInt64(&m.scannedDirs, 0)
	atomic.StoreInt64(&m.scannedFiles, 0)

	// Create new root node with same path and preserve filter state
	rootPath := m.root.Path
	m.root = &FileNode{
		Name:     filepath.Base(rootPath),
		Path:     rootPath,
		IsDir:    true,
		Expanded: true,
		Loading:  true,
	}
	// Use the new function that considers both filterRules and filterMap
	rootFilterPath := getFilterPath(rootPath)
	m.root.Filter = m.getEffectiveFilterWithMap(rootFilterPath)
	m.updateVisibleNodes()

	// Start async tree building
	go func() {
		m.buildTreeBreadthFirst(m.root, m.filterRules)
		// Send completion message
		if m.program != nil {
			m.program.Send(treeReadyMsg{root: m.root})
		}
	}()
}

// Breadth-first concurrent directory scanning
func (m *Model) buildTreeBreadthFirst(root *FileNode, filterRules []FilterRule) {
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

				children := m.scanSingleDirectory(node, m.filterRules)
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
func (m *Model) scanSingleDirectory(node *FileNode, filterRules []FilterRule) []*FileNode {
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

		// Get file info to capture size and modification time
		var modTime time.Time
		var size int64
		if info, err := entry.Info(); err == nil {
			modTime = info.ModTime()
			if !entry.IsDir() {
				size = info.Size()
			}
		}

		child := &FileNode{
			Name:    entry.Name(),
			Path:    childPath,
			IsDir:   entry.IsDir(),
			Size:    size,
			ModTime: modTime,
			Parent:  node,
		}

		childFilterPath := getFilterPath(childPath)
		child.Filter = m.getEffectiveFilterWithMap(childFilterPath)

		if !entry.IsDir() {
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

	// Sort children using the model's sort mode
	m.sortChildren(children)

	node.mu.Lock()
	node.Children = children
	node.Loading = false

	// Calculate stats for this directory now that children are loaded
	var totalSize int64
	var totalFiles int
	for _, child := range children {
		if child.IsDir {
			totalSize += child.TotalSize
			totalFiles += child.TotalFiles
		} else {
			totalSize += child.Size
			totalFiles++
		}
	}
	node.TotalSize = totalSize
	node.TotalFiles = totalFiles

	node.mu.Unlock()

	return childDirectories
}

func buildTreeRecursive(node *FileNode, filterRules []FilterRule) {
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

		// Get file info to capture size and modification time
		var modTime time.Time
		var size int64
		if info, err := entry.Info(); err == nil {
			modTime = info.ModTime()
			if !entry.IsDir() {
				size = info.Size()
			}
		}

		child := &FileNode{
			Name:    entry.Name(),
			Path:    childPath,
			IsDir:   entry.IsDir(),
			Size:    size,
			ModTime: modTime,
			Parent:  node,
		}

		childFilterPath := getFilterPath(childPath)
		child.Filter = getEffectiveFilter(childFilterPath, filterRules)

		node.Children = append(node.Children, child)

		if child.IsDir {
			buildTreeRecursive(child, filterRules)
		}
	}

	// This function is kept for compatibility but not used in async version
	// Sort would be handled by the caller if needed
}

func (m *Model) sortChildren(children []*FileNode) {
	sort.Slice(children, func(i, j int) bool {
		// Always put directories first
		if children[i].IsDir != children[j].IsDir {
			return children[i].IsDir
		}

		switch m.sortMode {
		case SortByName:
			return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
		case SortBySize:
			if children[i].IsDir && children[j].IsDir {
				return children[i].TotalSize > children[j].TotalSize
			}
			return children[i].Size > children[j].Size
		case SortByFileCount:
			if children[i].IsDir && children[j].IsDir {
				return children[i].TotalFiles > children[j].TotalFiles
			}
			// For files, sort by name since they don't have file counts
			return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
		case SortByLastModified:
			// Sort by modification time (most recent first)
			return children[i].ModTime.After(children[j].ModTime)
		default:
			return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
		}
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

func (m *Model) resortTree(node *FileNode) {
	if node.IsDir && len(node.Children) > 0 {
		m.sortChildren(node.Children)
		for _, child := range node.Children {
			m.resortTree(child)
		}
	}
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

	case refreshDirMsg:
		m.refreshDirectory()
		return m, tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
			return refreshMsg{}
		})

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
				saveFilterFile(m.filterFile, m.filterRules, m.filterMap)
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
			saveFilterFile(m.filterFile, m.filterRules, m.filterMap)
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

				// Create the appropriate filter pattern
				filterPath := getFilterPath(node.Path)
				if node.IsDir {
					// For directories, use /** to exclude the directory and all its contents
					filterPath = strings.TrimSuffix(filterPath, "/") + "/**"
				}

				// Normalize pattern to match original filter file format (without leading slash)
				filterPath = strings.TrimPrefix(filterPath, "/")

				m.filterMap[filterPath] = node.Filter
				if node.Filter == FilterNone {
					delete(m.filterMap, filterPath)
				}

				// Update children's filter status if this is a directory
				if node.IsDir {
					m.updateChildrenFilters(node)
				}
			}
			return m, nil

		case "i":
			m.invertSelection()
			return m, nil

		case "r":
			m.resetFilters()
			return m, nil

		case "1":
			m.sortMode = SortByName
			if m.root != nil {
				m.resortTree(m.root)
				m.updateVisibleNodes()
			}
			return m, nil

		case "2":
			m.sortMode = SortBySize
			if m.root != nil {
				m.resortTree(m.root)
				m.updateVisibleNodes()
			}
			return m, nil

		case "3":
			m.sortMode = SortByFileCount
			if m.root != nil {
				m.resortTree(m.root)
				m.updateVisibleNodes()
			}
			return m, nil

		case "4":
			m.sortMode = SortByLastModified
			if m.root != nil {
				m.resortTree(m.root)
				m.updateVisibleNodes()
			}
			return m, nil

		case "f5", "ctrl+r":
			return m, func() tea.Msg {
				return refreshDirMsg{}
			}
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
	// Collect directories that changed so we can update their children
	var changedDirs []*FileNode

	for _, node := range m.visibleNodes {
		switch node.Filter {
		case FilterNone:
			continue
		case FilterInclude:
			node.Filter = FilterExclude
		case FilterExclude:
			node.Filter = FilterInclude
		}

		// Create the appropriate filter pattern
		filterPath := getFilterPath(node.Path)
		if node.IsDir {
			// For directories, use /** to exclude the directory and all its contents
			filterPath = strings.TrimSuffix(filterPath, "/") + "/**"
			changedDirs = append(changedDirs, node)
		}

		if node.Filter == FilterNone {
			delete(m.filterMap, filterPath)
		} else {
			m.filterMap[filterPath] = node.Filter
		}
	}

	// Update children of all changed directories
	// TODO: Re-enable after debugging
	// for _, dir := range changedDirs {
	// 	m.updateChildrenFilters(dir)
	// }
}

func (m *Model) resetFilters() {
	for _, node := range m.visibleNodes {
		node.Filter = FilterNone
	}
	m.filterMap = make(map[string]FilterState)
}

// updateChildrenFilters recursively updates the filter status of all children
// based on the current filter rules including any new changes
func (m *Model) updateChildrenFilters(parent *FileNode) {
	if parent == nil || !parent.IsDir {
		return
	}

	// Simple approach: just update all children recursively with getEffectiveFilter
	m.updateChildrenRecursive(parent)
}

// updateChildrenRecursive updates filter status for all children
func (m *Model) updateChildrenRecursive(node *FileNode) {
	if node == nil || !node.IsDir {
		return
	}

	// Update all direct children
	node.mu.RLock()
	children := node.Children
	node.mu.RUnlock()

	for _, child := range children {
		// Update child's filter based on current filterMap and rules
		childFilterPath := getFilterPath(child.Path)
		child.Filter = m.getEffectiveFilterWithMap(childFilterPath)

		// If this child is a directory, update its children too
		if child.IsDir {
			m.updateChildrenRecursive(child)
		}
	}
}

// reapplyFiltersToTree recursively re-applies filters to all nodes in the tree
func (m *Model) reapplyFiltersToTree(node *FileNode) {
	if node == nil {
		return
	}

	// Update the current node's filter status
	filterPath := getFilterPath(node.Path)
	node.Filter = m.getEffectiveFilterWithMap(filterPath)

	// If this is a directory, recurse to all children
	if node.IsDir {
		node.mu.RLock()
		children := node.Children
		node.mu.RUnlock()

		for _, child := range children {
			m.reapplyFiltersToTree(child)
		}
	}
}

// getEffectiveFilterWithMap determines the effective filter state for a path
// considering both the original filterRules and the current filterMap changes
func (m *Model) getEffectiveFilterWithMap(path string) FilterState {
	// FIXED: Check for more specific patterns in filterMap FIRST
	// This ensures user's new patterns override existing ones correctly

	var bestMatch string
	var bestState FilterState = FilterNone
	var foundMatch bool

	// First, check all patterns in filterMap (including new user patterns)
	for pattern, state := range m.filterMap {
		if pattern == path || matchesRclonePattern(pattern, path) {
			// If this is a more specific match, use it
			if !foundMatch || len(pattern) > len(bestMatch) {
				bestMatch = pattern
				bestState = state
				foundMatch = true
			}
		}
	}

	// If we found a match in filterMap, return it
	if foundMatch {
		return bestState
	}

	// Fallback: check original rules for patterns not in filterMap
	for _, rule := range m.filterRules {
		if rule.Pattern == path || matchesRclonePattern(rule.Pattern, path) {
			// Only use this if it's not already handled by filterMap
			if _, exists := m.filterMap[rule.Pattern]; !exists {
				return rule.State
			}
		}
	}

	return FilterNone
}

// buildUpdatedFilterRules creates a new filter rules list that includes
// both the original rules and any new rules from the filterMap
func (m *Model) buildUpdatedFilterRules() []FilterRule {
	// Temporarily disabled to debug key issue
	return m.filterRules
}

// updateNodeFiltersRecursive recursively updates filter status for a node and all its children
func (m *Model) updateNodeFiltersRecursive(node *FileNode, filterRules []FilterRule) {
	// Temporarily disabled to debug key issue
	return
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

	var sortText string
	switch m.sortMode {
	case SortByName:
		sortText = "Sort: Name (1)"
	case SortBySize:
		sortText = "Sort: Size (2)"
	case SortByFileCount:
		sortText = "Sort: File Count (3)"
	case SortByLastModified:
		sortText = "Sort: Last Modified (4)"
	}

	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("Press ? for help, s to save, q to quit | " + sortText))
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
  ←           Collapse directory or go to parent
  → or Enter  Expand directory

Filters:
  Space       Toggle filter (none → include → exclude)
  i           Invert selection
  r           Reset all filters

Sorting:
  1           Sort by filename (default)
  2           Sort by size
  3           Sort by file count
  4           Sort by last modified

Other:
  ? or h      Show this help
  s           Save filters to file
  F5/Ctrl+R   Refresh directory tree
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
	} else {
		// Ensure rootPath is also absolute for proper comparison
		rootPath, _ = filepath.Abs(rootPath)
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

	// Special handling for /** patterns - they should match the directory itself
	// In rclone, "TV/**" matches both "TV" (the directory) and "TV/anything" (contents)
	if strings.HasSuffix(cleanPattern, "/**") {
		// Extract the directory part (everything before /**)
		dirPattern := strings.TrimSuffix(cleanPattern, "/**")

		// Check if the path exactly matches the directory
		if cleanPath == dirPattern {
			return true
		}

		// Check if the path is inside the directory (starts with dirPattern/)
		if strings.HasPrefix(cleanPath, dirPattern+"/") {
			return true
		}
	}

	// Convert rclone pattern to regex for other patterns
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
// using rclone's "first match wins" semantics with proper order
func getEffectiveFilter(path string, filterRules []FilterRule) FilterState {
	// Process rules in order - first match wins
	var matchedState FilterState = FilterNone

	for _, rule := range filterRules {
		if rule.Pattern == path || matchesRclonePattern(rule.Pattern, path) {
			matchedState = rule.State
			break
		}
	}

	// The pattern matching logic now handles /** patterns correctly,
	// so we don't need the UI enhancement anymore - just return the matched state
	return matchedState
}

func loadFilterFile(filename string) ([]FilterRule, map[string]FilterState) {
	var filterRules []FilterRule
	filterMap := make(map[string]FilterState)

	file, err := os.Open(filename)
	if err != nil {
		return filterRules, filterMap
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
			filterRules = append(filterRules, FilterRule{Pattern: path, State: FilterInclude})
			filterMap[path] = FilterInclude
		} else if strings.HasPrefix(line, "- ") {
			path := strings.TrimPrefix(line, "- ")
			filterRules = append(filterRules, FilterRule{Pattern: path, State: FilterExclude})
			filterMap[path] = FilterExclude
		}
	}

	return filterRules, filterMap
}

func saveFilterFile(filename string, filterRules []FilterRule, filterMap map[string]FilterState) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	writtenPaths := make(map[string]bool)

	// Build list of new rules that need to be inserted
	newRules := make(map[string]FilterState)
	for path, state := range filterMap {
		// Check if this path was in the original rules
		found := false
		for _, rule := range filterRules {
			if rule.Pattern == path {
				found = true
				break
			}
		}
		if !found {
			newRules[path] = state
		}
	}

	// Write rules in original order, inserting new rules at appropriate positions
	for i, rule := range filterRules {
		// Write existing rule if it still exists in filterMap
		if currentState, exists := filterMap[rule.Pattern]; exists {
			switch currentState {
			case FilterInclude:
				fmt.Fprintf(writer, "+ %s\n", rule.Pattern)
			case FilterExclude:
				fmt.Fprintf(writer, "- %s\n", rule.Pattern)
			}
			writtenPaths[rule.Pattern] = true
		}

		// After writing this rule, check if we should insert any new rules before the next rule
		// Insert new rules that should come before more general patterns
		if i+1 < len(filterRules) {
			nextRule := filterRules[i+1]

			// Insert new rules that are more specific than the next rule
			for newPath, newState := range newRules {
				if !writtenPaths[newPath] && shouldInsertBefore(newPath, nextRule.Pattern) {
					switch newState {
					case FilterInclude:
						fmt.Fprintf(writer, "+ %s\n", newPath)
					case FilterExclude:
						fmt.Fprintf(writer, "- %s\n", newPath)
					}
					writtenPaths[newPath] = true
				}
			}
		}
	}

	// Write any remaining new rules that weren't inserted above
	for path, state := range newRules {
		if !writtenPaths[path] {
			switch state {
			case FilterInclude:
				fmt.Fprintf(writer, "+ %s\n", path)
			case FilterExclude:
				fmt.Fprintf(writer, "- %s\n", path)
			}
		}
	}

	return writer.Flush()
}

// shouldInsertBefore determines if a new rule should be inserted before an existing rule
// More specific patterns should come before more general ones
func shouldInsertBefore(newPattern, existingPattern string) bool {
	// Special case: anything should come before the catch-all "*" pattern
	if existingPattern == "*" {
		return true
	}

	// If the new pattern is more specific than the existing pattern, it should come first
	// More specific means: longer path, or same directory but more specific pattern

	// Extract directory prefixes
	newDir := getPatternDirectory(newPattern)
	existingDir := getPatternDirectory(existingPattern)

	// If they're in the same directory, more specific patterns go first
	if newDir == existingDir {
		// More specific patterns (longer, more detailed) should come first
		return len(newPattern) > len(existingPattern) ||
			(strings.Contains(newPattern, "/") && !strings.Contains(existingPattern, "/**"))
	}

	// If the new pattern is a subdirectory of the existing pattern's directory, it should come first
	if existingDir != "" && strings.HasPrefix(newDir, existingDir) {
		return true
	}

	return false
}

// getPatternDirectory extracts the directory part of a pattern
func getPatternDirectory(pattern string) string {
	// Remove leading slashes and wildcards
	pattern = strings.TrimPrefix(pattern, "/")

	// For patterns like "TV/**" return "TV"
	if strings.HasSuffix(pattern, "/**") {
		return strings.TrimSuffix(pattern, "/**")
	}

	// For patterns like "TV/Show Name/**" return "TV"
	parts := strings.Split(pattern, "/")
	if len(parts) > 0 {
		return parts[0]
	}

	return ""
}
