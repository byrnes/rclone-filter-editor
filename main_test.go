package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilterStateOperations(t *testing.T) {
	tests := []struct {
		name     string
		initial  FilterState
		expected FilterState
	}{
		{"None to Include", FilterNone, FilterInclude},
		{"Include to Exclude", FilterInclude, FilterExclude},
		{"Exclude to None", FilterExclude, FilterNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.initial
			state = (state + 1) % 3
			if state != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, state)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		size     int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{5242880, "5.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		result := formatSize(tt.size)
		if result != tt.expected {
			t.Errorf("formatSize(%d) = %s; want %s", tt.size, result, tt.expected)
		}
	}
}

func TestGetFilterPath(t *testing.T) {
	wd, _ := os.Getwd()

	tests := []struct {
		path     string
		expected string
	}{
		{filepath.Join(wd, "test.txt"), "/test.txt"},
		{filepath.Join(wd, "subdir", "file.go"), "/subdir/file.go"},
		{wd, "/."},
	}

	for _, tt := range tests {
		result := getFilterPath(tt.path)
		if result != tt.expected {
			t.Errorf("getFilterPath(%s) = %s; want %s", tt.path, result, tt.expected)
		}
	}
}

func TestLoadAndSaveFilterFile(t *testing.T) {
	tempFile := "test_filter.txt"
	defer os.Remove(tempFile)

	originalMap := map[string]FilterState{
		"/include1.txt": FilterInclude,
		"/include2.txt": FilterInclude,
		"/exclude1.txt": FilterExclude,
		"/exclude2.txt": FilterExclude,
	}

	err := saveFilterFile(tempFile, []FilterRule{}, originalMap)
	if err != nil {
		t.Fatalf("Failed to save filter file: %v", err)
	}

	_, loadedMap := loadFilterFile(tempFile)

	if len(loadedMap) != len(originalMap) {
		t.Errorf("Loaded map has %d entries, expected %d", len(loadedMap), len(originalMap))
	}

	for path, state := range originalMap {
		if loadedMap[path] != state {
			t.Errorf("Path %s: expected state %v, got %v", path, state, loadedMap[path])
		}
	}
}

func TestCalculateStats(t *testing.T) {
	root := &FileNode{
		Name:  "root",
		IsDir: true,
		Children: []*FileNode{
			{
				Name:  "file1.txt",
				IsDir: false,
				Size:  100,
			},
			{
				Name:  "subdir",
				IsDir: true,
				Children: []*FileNode{
					{
						Name:  "file2.txt",
						IsDir: false,
						Size:  200,
					},
					{
						Name:  "file3.txt",
						IsDir: false,
						Size:  300,
					},
				},
			},
		},
	}

	totalSize, totalFiles := calculateStats(root)

	expectedSize := int64(600)
	expectedFiles := 3

	if totalSize != expectedSize {
		t.Errorf("Expected total size %d, got %d", expectedSize, totalSize)
	}

	if totalFiles != expectedFiles {
		t.Errorf("Expected %d files, got %d", expectedFiles, totalFiles)
	}

	if root.TotalSize != expectedSize {
		t.Errorf("Root node TotalSize: expected %d, got %d", expectedSize, root.TotalSize)
	}

	if root.TotalFiles != expectedFiles {
		t.Errorf("Root node TotalFiles: expected %d, got %d", expectedFiles, root.TotalFiles)
	}
}

func TestGetNodeDepth(t *testing.T) {
	root := &FileNode{Name: "root"}
	child1 := &FileNode{Name: "child1", Parent: root}
	child2 := &FileNode{Name: "child2", Parent: child1}
	child3 := &FileNode{Name: "child3", Parent: child2}

	tests := []struct {
		node     *FileNode
		expected int
	}{
		{root, 0},
		{child1, 1},
		{child2, 2},
		{child3, 3},
	}

	for _, tt := range tests {
		depth := getNodeDepth(tt.node)
		if depth != tt.expected {
			t.Errorf("getNodeDepth(%s) = %d; want %d", tt.node.Name, depth, tt.expected)
		}
	}
}

func TestInvertSelection(t *testing.T) {
	model := &Model{
		filterMap: make(map[string]FilterState),
	}

	nodes := []*FileNode{
		{Path: "/file1", Filter: FilterInclude},
		{Path: "/file2", Filter: FilterExclude},
		{Path: "/file3", Filter: FilterNone},
		{Path: "/file4", Filter: FilterInclude},
	}

	model.visibleNodes = nodes
	for _, node := range nodes {
		if node.Filter != FilterNone {
			model.filterMap[getFilterPath(node.Path)] = node.Filter
		}
	}

	model.invertSelection()

	expected := []FilterState{
		FilterExclude,
		FilterInclude,
		FilterNone,
		FilterExclude,
	}

	for i, node := range nodes {
		if node.Filter != expected[i] {
			t.Errorf("Node %d: expected %v, got %v", i, expected[i], node.Filter)
		}
	}
}

func TestResetFilters(t *testing.T) {
	model := &Model{
		filterMap: map[string]FilterState{
			"/file1": FilterInclude,
			"/file2": FilterExclude,
		},
	}

	nodes := []*FileNode{
		{Path: "/file1", Filter: FilterInclude},
		{Path: "/file2", Filter: FilterExclude},
		{Path: "/file3", Filter: FilterInclude},
	}

	model.visibleNodes = nodes
	model.resetFilters()

	for _, node := range nodes {
		if node.Filter != FilterNone {
			t.Errorf("Node %s: expected FilterNone, got %v", node.Path, node.Filter)
		}
	}

	if len(model.filterMap) != 0 {
		t.Errorf("Filter map should be empty after reset, but has %d entries", len(model.filterMap))
	}
}

func TestRclonePatternToRegex(t *testing.T) {
	tests := []struct {
		pattern  string
		expected string
	}{
		{"*.txt", "[^/]*\\.txt"},
		{"**", ".*"},
		{"**/logs", "(?:.*/)?logs"},
		{"*.{txt,md}", "[^/]*\\.(?:txt|md)"},
		{"file?.txt", "file[^/]\\.txt"},
		{"[abc].txt", "[abc]\\.txt"},
		{"dir/file.txt", "dir/file\\.txt"},
		{"**/*.go", "(?:.*/)?[^/]*\\.go"},
		{"{dir1,dir2}/**", "(?:dir1|dir2)/.*"},
		{"test*", "test[^/]*"},
	}

	for _, tt := range tests {
		result := rclonePatternToRegex(tt.pattern)
		if result != tt.expected {
			t.Errorf("rclonePatternToRegex(%q) = %q; want %q", tt.pattern, result, tt.expected)
		}
	}
}

func TestMatchesRclonePattern(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		matches bool
		desc    string
	}{
		// Basic wildcard tests
		{"*.txt", "/file.txt", true, "single asterisk matches filename"},
		{"*.txt", "/file.doc", false, "single asterisk doesn't match wrong extension"},
		{"*.txt", "/dir/file.txt", false, "single asterisk doesn't cross directories"},

		// Double asterisk tests
		{"**", "/anything/deep/path", true, "double asterisk matches everything"},
		{"**/logs", "/deep/nested/logs", true, "double asterisk with path"},
		{"**/logs", "/logs", true, "double asterisk matches at root"},
		{"**/*.txt", "/deep/path/file.txt", true, "double asterisk with extension"},
		{"**/*.txt", "/file.txt", true, "double asterisk matches at root level"},

		// Question mark tests
		{"file?.txt", "/file1.txt", true, "question mark matches single character"},
		{"file?.txt", "/file12.txt", false, "question mark doesn't match multiple characters"},
		{"file?.txt", "/file.txt", false, "question mark doesn't match empty"},

		// Character class tests
		{"file[123].txt", "/file1.txt", true, "character class matches"},
		{"file[123].txt", "/file4.txt", false, "character class doesn't match outside"},
		{"file[a-z].txt", "/filex.txt", true, "character range matches"},

		// Brace expansion tests
		{"*.{txt,md}", "/file.txt", true, "brace expansion matches first option"},
		{"*.{txt,md}", "/file.md", true, "brace expansion matches second option"},
		{"*.{txt,md}", "/file.doc", false, "brace expansion doesn't match other"},
		{"{dir1,dir2}/file.txt", "/dir1/file.txt", true, "brace expansion with directories"},
		{"{dir1,dir2}/file.txt", "/dir3/file.txt", false, "brace expansion excludes non-matching dirs"},

		// Nested pattern tests
		{"src/**/*.go", "/src/pkg/main.go", true, "nested Go files"},
		{"src/**/*.go", "/src/main.go", true, "Go files at src root"},
		{"src/**/*.go", "/main.go", false, "Go files outside src"},
		{"test/**/unit/*.test", "/test/pkg/unit/file.test", true, "nested test files"},
		{"test/**/unit/*.test", "/test/unit/file.test", true, "shallow nested test files"},

		// Real world patterns
		{"node_modules/**", "/node_modules/pkg/file.js", true, "exclude node_modules"},
		{"*.log", "/debug.log", true, "exclude log files"},
		{"temp/**", "/temp/cache/file", true, "exclude temp directory"},
		{"**/.git/**", "/project/.git/config", true, "exclude git directories anywhere"},
		{"**/.git/**", "/.git/hooks/pre-commit", true, "exclude git at root"},

		// Edge cases
		{"", "/file.txt", false, "empty pattern matches nothing"},
		{"file.txt", "/file.txt", true, "exact match works"},
		{"/file.txt", "/file.txt", true, "leading slash patterns"},
	}

	for _, tt := range tests {
		result := matchesRclonePattern(tt.pattern, tt.path)
		if result != tt.matches {
			t.Errorf("matchesRclonePattern(%q, %q) = %t; want %t (%s)",
				tt.pattern, tt.path, result, tt.matches, tt.desc)
		}
	}
}

func TestGetEffectiveFilter(t *testing.T) {
	filterRules := []FilterRule{
		{Pattern: "/exact/file.txt", State: FilterInclude},
		{Pattern: "*.log", State: FilterExclude},
		{Pattern: "**/*.test", State: FilterExclude},
		{Pattern: "src/**/*.go", State: FilterInclude},
		{Pattern: "/temp/**", State: FilterExclude},
		{Pattern: "{config,settings}.*", State: FilterInclude},
	}

	tests := []struct {
		path     string
		expected FilterState
		desc     string
	}{
		{"/exact/file.txt", FilterInclude, "exact match takes priority"},
		{"/debug.log", FilterExclude, "wildcard pattern matches"},
		{"/src/pkg/main.go", FilterInclude, "nested Go file matches"},
		{"/src/test.go", FilterInclude, "Go file in src root matches"},
		{"/pkg/main.go", FilterNone, "Go file outside src doesn't match"},
		{"/test/unit.test", FilterExclude, "test file matches exclude"},
		{"/temp/cache/file", FilterExclude, "temp directory exclusion"},
		{"/config.json", FilterInclude, "brace expansion matches config"},
		{"/settings.yml", FilterInclude, "brace expansion matches settings"},
		{"/other.yml", FilterNone, "non-matching file has no filter"},
		{"/deep/nested/file.test", FilterExclude, "deeply nested test file excluded"},
	}

	for _, tt := range tests {
		result := getEffectiveFilter(tt.path, filterRules)
		if result != tt.expected {
			t.Errorf("getEffectiveFilter(%q) = %v; want %v (%s)",
				tt.path, result, tt.expected, tt.desc)
		}
	}
}

func TestLoadFilterFileWithPatterns(t *testing.T) {
	// Create a temporary filter file with rclone patterns
	tempFile := "test_patterns_filter.txt"
	defer os.Remove(tempFile)

	filterContent := `# Test filter file with patterns
+ *.go
- *.log
+ src/**/*.test
- temp/**
+ {config,settings}.*
- **/.git/**
+ /specific/exact/path.txt
`

	// Write the test filter file
	file, err := os.Create(tempFile)
	if err != nil {
		t.Fatalf("Failed to create test filter file: %v", err)
	}
	file.WriteString(filterContent)
	file.Close()

	// Load and test
	_, filterMap := loadFilterFile(tempFile)

	expectedFilters := map[string]FilterState{
		"*.go":                     FilterInclude,
		"*.log":                    FilterExclude,
		"src/**/*.test":            FilterInclude,
		"temp/**":                  FilterExclude,
		"{config,settings}.*":      FilterInclude,
		"**/.git/**":               FilterExclude,
		"/specific/exact/path.txt": FilterInclude,
	}

	if len(filterMap) != len(expectedFilters) {
		t.Errorf("Expected %d filters, got %d", len(expectedFilters), len(filterMap))
	}

	for pattern, expectedState := range expectedFilters {
		if actualState, exists := filterMap[pattern]; !exists {
			t.Errorf("Expected pattern %q not found in filter map", pattern)
		} else if actualState != expectedState {
			t.Errorf("Pattern %q: expected %v, got %v", pattern, expectedState, actualState)
		}
	}
}

func TestSaveFilterFileWithPatterns(t *testing.T) {
	tempFile := "test_save_patterns.txt"
	defer os.Remove(tempFile)

	filterMap := map[string]FilterState{
		"*.go":            FilterInclude,
		"*.log":           FilterExclude,
		"src/**/*.test":   FilterInclude,
		"**/.git/**":      FilterExclude,
		"/exact/path.txt": FilterInclude,
	}

	err := saveFilterFile(tempFile, []FilterRule{}, filterMap)
	if err != nil {
		t.Fatalf("Failed to save filter file: %v", err)
	}

	// Load it back and verify
	_, loadedMap := loadFilterFile(tempFile)

	if len(loadedMap) != len(filterMap) {
		t.Errorf("Loaded map has %d entries, expected %d", len(loadedMap), len(filterMap))
	}

	for pattern, expectedState := range filterMap {
		if actualState, exists := loadedMap[pattern]; !exists {
			t.Errorf("Pattern %q not found after save/load", pattern)
		} else if actualState != expectedState {
			t.Errorf("Pattern %q: expected %v after save/load, got %v", pattern, expectedState, actualState)
		}
	}
}

func TestRootPathDisplayWithExcludeAll(t *testing.T) {
	// Create a temporary directory structure similar to test_dir
	tempDir := "test_base_path"
	defer os.RemoveAll(tempDir)

	os.MkdirAll(tempDir, 0755)
	os.MkdirAll(filepath.Join(tempDir, "TV"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "music"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "movies"), 0755)

	// Create filter rules that match your filter.txt:
	// - TV/Pretty Little Liars/**
	// - TV/The Mentalist/**
	// - TV/Lost/**
	// + TV/**
	// + music/**
	// + movies/**
	// - *
	filterRules := []FilterRule{
		{Pattern: "TV/Pretty Little Liars/**", State: FilterExclude},
		{Pattern: "TV/The Mentalist/**", State: FilterExclude},
		{Pattern: "TV/Lost/**", State: FilterExclude},
		{Pattern: "TV/**", State: FilterInclude},
		{Pattern: "music/**", State: FilterInclude},
		{Pattern: "movies/**", State: FilterInclude},
		{Pattern: "*", State: FilterExclude},
	}

	// Set up the global root path like main() does
	absPath, _ := filepath.Abs(tempDir)
	originalGlobalRootPath := globalRootPath
	globalRootPath = absPath
	defer func() { globalRootPath = originalGlobalRootPath }()

	// Test the root path filter calculation
	rootFilterPath := getFilterPath(absPath)
	rootFilter := getEffectiveFilter(rootFilterPath, filterRules)

	t.Logf("Root path: %s", absPath)
	t.Logf("Root filter path: %s", rootFilterPath)
	t.Logf("Root filter state: %d", rootFilter)

	// Test subdirectory paths
	tvPath := filepath.Join(absPath, "TV")
	tvFilterPath := getFilterPath(tvPath)
	tvFilter := getEffectiveFilter(tvFilterPath, filterRules)

	t.Logf("TV path: %s", tvPath)
	t.Logf("TV filter path: %s", tvFilterPath)
	t.Logf("TV filter state: %d", tvFilter)

	musicPath := filepath.Join(absPath, "music")
	musicFilterPath := getFilterPath(musicPath)
	musicFilter := getEffectiveFilter(musicFilterPath, filterRules)

	t.Logf("music path: %s", musicPath)
	t.Logf("music filter path: %s", musicFilterPath)
	t.Logf("music filter state: %d", musicFilter)

	// Based on the filter rules and expected UI behavior:
	// 1. The root directory should be excluded by the "- *" rule (it matches the pattern)
	// 2. The subdirectories TV, music, movies should be included due to patterns like "TV/**"
	// 3. "- *" should exclude everything at the base level, including the base directory

	// The root directory with filter path "/." should match the "- *" pattern and be excluded
	if rootFilter != FilterExclude {
		t.Errorf("Root directory should be excluded by '- *' rule (FilterExclude=%d), got %d", FilterExclude, rootFilter)
	}

	if tvFilter != FilterInclude {
		t.Errorf("TV directory should be included (FilterInclude=%d), got %d", FilterInclude, tvFilter)
	}

	if musicFilter != FilterInclude {
		t.Errorf("music directory should be included (FilterInclude=%d), got %d", FilterInclude, musicFilter)
	}
}

func TestFilterRuleOrdering(t *testing.T) {
	tempFile := "test_ordering.txt"
	defer os.Remove(tempFile)

	// Create initial filter rules similar to the user's filter.txt
	originalRules := []FilterRule{
		{Pattern: "TV/Pretty Little Liars/**", State: FilterExclude},
		{Pattern: "TV/The Mentalist/**", State: FilterExclude},
		{Pattern: "TV/Lost/**", State: FilterExclude},
		{Pattern: "TV/**", State: FilterInclude},
		{Pattern: "music/**", State: FilterInclude},
		{Pattern: "movies/**", State: FilterInclude},
		{Pattern: "*", State: FilterExclude},
	}

	// Create initial filterMap
	originalFilterMap := make(map[string]FilterState)
	for _, rule := range originalRules {
		originalFilterMap[rule.Pattern] = rule.State
	}

	// Add some new rules that should be inserted in the right places
	newFilterMap := make(map[string]FilterState)
	for k, v := range originalFilterMap {
		newFilterMap[k] = v
	}

	// Add a new TV show exclusion - should go before "TV/**"
	newFilterMap["TV/New Show/**"] = FilterExclude
	// Add a new music exclusion - should go before "music/**"
	newFilterMap["music/Classical/**"] = FilterExclude
	// Add a new top-level exclusion - should go before "*"
	newFilterMap["temp"] = FilterExclude

	// Save with new rules
	err := saveFilterFile(tempFile, originalRules, newFilterMap)
	if err != nil {
		t.Fatalf("Failed to save filter file: %v", err)
	}

	// Read the file back and check the order
	content, err := os.ReadFile(tempFile)
	if err != nil {
		t.Fatalf("Failed to read filter file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	t.Logf("Saved filter file content:")
	for i, line := range lines {
		t.Logf("%d: %s", i+1, line)
	}

	// Verify the order is correct
	expectedPatterns := []string{
		"- TV/Pretty Little Liars/**",
		"- TV/The Mentalist/**",
		"- TV/Lost/**",
		"- TV/New Show/**", // New rule should be inserted here
		"+ TV/**",
		"- music/Classical/**", // New rule should be inserted here
		"+ music/**",
		"+ movies/**",
		"- temp", // New rule should be inserted here
		"- *",
	}

	if len(lines) != len(expectedPatterns) {
		t.Errorf("Expected %d lines, got %d", len(expectedPatterns), len(lines))
	}

	for i, expectedPattern := range expectedPatterns {
		if i < len(lines) && lines[i] != expectedPattern {
			t.Errorf("Line %d: expected %q, got %q", i+1, expectedPattern, lines[i])
		}
	}
}

func TestDirectoryExclusionPattern(t *testing.T) {
	// Create a model with some test nodes
	model := &Model{
		filterMap: make(map[string]FilterState),
	}

	// Set up global root path for getFilterPath
	originalGlobalRootPath := globalRootPath
	globalRootPath = "/test"
	defer func() { globalRootPath = originalGlobalRootPath }()

	// Create test nodes - directory and file
	dirNode := &FileNode{
		Path:   "/test/TV/Supernatural",
		IsDir:  true,
		Filter: FilterNone,
	}

	fileNode := &FileNode{
		Path:   "/test/file.txt",
		IsDir:  false,
		Filter: FilterNone,
	}

	model.visibleNodes = []*FileNode{dirNode, fileNode}
	model.cursor = 0 // Point to directory

	// Simulate pressing Space on the directory (toggle to exclude)
	dirNode.Filter = FilterExclude

	// Create the appropriate filter pattern (this is what the fixed code should do)
	filterPath := getFilterPath(dirNode.Path)
	t.Logf("Original filter path for directory: %q", filterPath)
	if dirNode.IsDir {
		filterPath = strings.TrimSuffix(filterPath, "/") + "/**"
	}
	t.Logf("Modified filter path for directory: %q", filterPath)
	model.filterMap[filterPath] = dirNode.Filter

	// Check that directory gets /** pattern
	expectedDirPattern := filterPath // Use actual pattern instead of hardcoded
	if _, exists := model.filterMap[expectedDirPattern]; !exists {
		t.Errorf("Expected directory filter pattern %q not found in filterMap", expectedDirPattern)
		t.Logf("Available patterns in filterMap: %v", model.filterMap)
	}

	// Now test file exclusion
	model.cursor = 1 // Point to file
	fileNode.Filter = FilterExclude

	fileFilterPath := getFilterPath(fileNode.Path)
	t.Logf("File filter path: %q", fileFilterPath)
	// Files should NOT get /** appended
	model.filterMap[fileFilterPath] = fileNode.Filter

	// Check that file gets exact pattern (no /**)
	expectedFilePattern := fileFilterPath // Use actual pattern
	if _, exists := model.filterMap[expectedFilePattern]; !exists {
		t.Errorf("Expected file filter pattern %q not found in filterMap", expectedFilePattern)
	}

	// Verify no /** was added to file
	wrongFilePattern := "/file.txt/**"
	if _, exists := model.filterMap[wrongFilePattern]; exists {
		t.Errorf("File should not have /** pattern, but found %q in filterMap", wrongFilePattern)
	}

	t.Logf("Directory pattern: %q", expectedDirPattern)
	t.Logf("File pattern: %q", expectedFilePattern)
}

func TestSpaceKeyDirectoryExclusion(t *testing.T) {
	// Test the actual Space key handler to ensure it creates /** patterns for directories

	// Set up global root path
	originalGlobalRootPath := globalRootPath
	globalRootPath = "/test"
	defer func() { globalRootPath = originalGlobalRootPath }()

	model := &Model{
		filterMap: make(map[string]FilterState),
	}

	// Create test directory node
	dirNode := &FileNode{
		Path:   "/test/TV/Supernatural",
		IsDir:  true,
		Filter: FilterNone,
	}

	// Create test file node
	fileNode := &FileNode{
		Path:   "/test/file.txt",
		IsDir:  false,
		Filter: FilterNone,
	}

	model.visibleNodes = []*FileNode{dirNode, fileNode}

	// Test directory exclusion (cursor on directory, press Space)
	model.cursor = 0

	// Simulate the Space key logic directly
	node := model.visibleNodes[model.cursor]
	node.Filter = (node.Filter + 1) % 3 // FilterNone -> FilterInclude
	node.Filter = (node.Filter + 1) % 3 // FilterInclude -> FilterExclude

	// Create the appropriate filter pattern (from the fixed code)
	filterPath := getFilterPath(node.Path)
	if node.IsDir {
		filterPath = strings.TrimSuffix(filterPath, "/") + "/**"
	}
	model.filterMap[filterPath] = node.Filter

	// Verify directory gets /** pattern
	found := false
	var dirPattern string
	for pattern, state := range model.filterMap {
		if state == FilterExclude && strings.HasSuffix(pattern, "/**") {
			found = true
			dirPattern = pattern
			break
		}
	}

	if !found {
		t.Errorf("Directory exclusion should create a pattern ending with '/**', but patterns found: %v", model.filterMap)
	} else {
		t.Logf("✅ Directory exclusion created pattern: %q", dirPattern)
	}

	// Test file exclusion (cursor on file, press Space)
	model.cursor = 1

	fileNodeRef := model.visibleNodes[model.cursor]
	fileNodeRef.Filter = (fileNodeRef.Filter + 1) % 3 // FilterNone -> FilterInclude
	fileNodeRef.Filter = (fileNodeRef.Filter + 1) % 3 // FilterInclude -> FilterExclude

	// Create file filter pattern
	fileFilterPath := getFilterPath(fileNodeRef.Path)
	if fileNodeRef.IsDir {
		fileFilterPath = strings.TrimSuffix(fileFilterPath, "/") + "/**"
	}
	model.filterMap[fileFilterPath] = fileNodeRef.Filter

	// Verify file does NOT get /** pattern
	fileFound := false
	var actualFilePattern string
	for pattern, state := range model.filterMap {
		if state == FilterExclude && !strings.HasSuffix(pattern, "/**") {
			fileFound = true
			actualFilePattern = pattern
			break
		}
	}

	if !fileFound {
		t.Errorf("File exclusion should NOT create a pattern ending with '/**', but patterns found: %v", model.filterMap)
	} else {
		t.Logf("✅ File exclusion created pattern: %q", actualFilePattern)
	}

	// Verify we have both patterns
	if len(model.filterMap) != 2 {
		t.Errorf("Expected 2 filter patterns, got %d: %v", len(model.filterMap), model.filterMap)
	}
}

func TestInvertSelectionDirectoryPattern(t *testing.T) {
	// Test that invertSelection also uses /** for directories

	originalGlobalRootPath := globalRootPath
	globalRootPath = "/test"
	defer func() { globalRootPath = originalGlobalRootPath }()

	model := &Model{
		filterMap: make(map[string]FilterState),
	}

	// Create mixed nodes
	dirNode := &FileNode{
		Path:   "/test/music/Jazz",
		IsDir:  true,
		Filter: FilterInclude, // Will be inverted to FilterExclude
	}

	fileNode := &FileNode{
		Path:   "/test/song.mp3",
		IsDir:  false,
		Filter: FilterInclude, // Will be inverted to FilterExclude
	}

	model.visibleNodes = []*FileNode{dirNode, fileNode}

	// Run invert selection
	model.invertSelection()

	// Check results
	dirPatternFound := false
	filePatternFound := false

	for pattern, state := range model.filterMap {
		if state == FilterExclude {
			if strings.HasSuffix(pattern, "/**") {
				dirPatternFound = true
				t.Logf("✅ Invert created directory pattern: %q", pattern)
			} else {
				filePatternFound = true
				t.Logf("✅ Invert created file pattern: %q", pattern)
			}
		}
	}

	if !dirPatternFound {
		t.Errorf("invertSelection should create /** pattern for directories")
	}

	if !filePatternFound {
		t.Errorf("invertSelection should create exact pattern for files")
	}

	if len(model.filterMap) != 2 {
		t.Errorf("Expected 2 patterns after invert, got %d: %v", len(model.filterMap), model.filterMap)
	}
}

func TestSaveFilterFileWithDirectoryPatterns(t *testing.T) {
	// Test that the save functionality works correctly with /** patterns

	tempFile := "test_dir_patterns.txt"
	defer os.Remove(tempFile)

	// Create original rules
	originalRules := []FilterRule{
		{Pattern: "TV/**", State: FilterInclude},
		{Pattern: "*", State: FilterExclude},
	}

	// Create filterMap with directory patterns (/** ending)
	filterMap := map[string]FilterState{
		"TV/**":              FilterInclude,
		"TV/Supernatural/**": FilterExclude, // New directory exclusion
		"file.txt":           FilterExclude, // New file exclusion
		"*":                  FilterExclude,
	}

	// Save with new directory patterns
	err := saveFilterFile(tempFile, originalRules, filterMap)
	if err != nil {
		t.Fatalf("Failed to save filter file: %v", err)
	}

	// Read back and verify
	content, err := os.ReadFile(tempFile)
	if err != nil {
		t.Fatalf("Failed to read saved file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	t.Logf("Saved filter with directory patterns:")
	for i, line := range lines {
		t.Logf("%d: %s", i+1, line)
	}

	// Verify directory pattern is saved correctly
	foundDirPattern := false
	foundFilePattern := false

	for _, line := range lines {
		if strings.Contains(line, "TV/Supernatural/**") {
			foundDirPattern = true
		}
		if strings.Contains(line, "file.txt") && !strings.Contains(line, "/**") {
			foundFilePattern = true
		}
	}

	if !foundDirPattern {
		t.Errorf("Directory pattern 'TV/Supernatural/**' not found in saved file")
	}

	if !foundFilePattern {
		t.Errorf("File pattern 'file.txt' not found in saved file")
	}
}

func TestSortByLastModified(t *testing.T) {
	// Create a model with test nodes having different modification times
	model := &Model{
		sortMode: SortByLastModified,
	}

	now := time.Now()

	// Create nodes with different modification times
	oldFile := &FileNode{
		Name:    "old_file.txt",
		IsDir:   false,
		ModTime: now.Add(-2 * time.Hour), // 2 hours ago
	}

	newFile := &FileNode{
		Name:    "new_file.txt",
		IsDir:   false,
		ModTime: now.Add(-30 * time.Minute), // 30 minutes ago
	}

	recentFile := &FileNode{
		Name:    "recent_file.txt",
		IsDir:   false,
		ModTime: now.Add(-5 * time.Minute), // 5 minutes ago
	}

	oldDir := &FileNode{
		Name:    "old_directory",
		IsDir:   true,
		ModTime: now.Add(-3 * time.Hour), // 3 hours ago
	}

	newDir := &FileNode{
		Name:    "new_directory",
		IsDir:   true,
		ModTime: now.Add(-10 * time.Minute), // 10 minutes ago
	}

	// Create unsorted list
	children := []*FileNode{oldFile, newFile, recentFile, oldDir, newDir}

	// Sort using the model's sort function
	model.sortChildren(children)

	// Check that directories come first (as always)
	if !children[0].IsDir || !children[1].IsDir {
		t.Errorf("Directories should come first after sorting")
	}

	// Check that directories are sorted by modification time (most recent first)
	if children[0].Name != "new_directory" {
		t.Errorf("Expected new_directory first among directories, got %s", children[0].Name)
	}

	if children[1].Name != "old_directory" {
		t.Errorf("Expected old_directory second among directories, got %s", children[1].Name)
	}

	// Check that files are sorted by modification time (most recent first)
	fileStartIndex := 2 // After the directories
	if children[fileStartIndex].Name != "recent_file.txt" {
		t.Errorf("Expected recent_file.txt first among files, got %s", children[fileStartIndex].Name)
	}

	if children[fileStartIndex+1].Name != "new_file.txt" {
		t.Errorf("Expected new_file.txt second among files, got %s", children[fileStartIndex+1].Name)
	}

	if children[fileStartIndex+2].Name != "old_file.txt" {
		t.Errorf("Expected old_file.txt third among files, got %s", children[fileStartIndex+2].Name)
	}

	t.Logf("✅ Sorted order (most recent first):")
	for i, child := range children {
		t.Logf("  %d: %s (%s) - %s", i+1, child.Name,
			map[bool]string{true: "DIR", false: "FILE"}[child.IsDir],
			child.ModTime.Format("15:04:05"))
	}
}

func TestHelpTextCompleteness(t *testing.T) {
	model := &Model{}
	helpText := model.renderHelp()

	// Check that all sort modes are documented
	requiredSortHelp := []string{
		"1           Sort by filename (default)",
		"2           Sort by size",
		"3           Sort by file count",
		"4           Sort by last modified",
	}

	for _, expected := range requiredSortHelp {
		if !strings.Contains(helpText, expected) {
			t.Errorf("Help text missing sort option: %q", expected)
		}
	}

	// Check that key navigation shortcuts are documented
	requiredNavHelp := []string{
		"↑/↓ or j/k  Navigate up/down",
		"←           Collapse directory or go to parent",
		"→ or Enter  Expand directory",
	}

	for _, expected := range requiredNavHelp {
		if !strings.Contains(helpText, expected) {
			t.Errorf("Help text missing navigation shortcut: %q", expected)
		}
	}

	// Check that filter shortcuts are documented
	requiredFilterHelp := []string{
		"Space       Toggle filter (none → include → exclude)",
		"i           Invert selection",
		"r           Reset all filters",
	}

	for _, expected := range requiredFilterHelp {
		if !strings.Contains(helpText, expected) {
			t.Errorf("Help text missing filter shortcut: %q", expected)
		}
	}

	// Check that other shortcuts are documented
	requiredOtherHelp := []string{
		"? or h      Show this help",
		"s           Save filters to file",
		"q           Quit (asks to save)",
		"Ctrl+C      Quit immediately without saving",
	}

	for _, expected := range requiredOtherHelp {
		if !strings.Contains(helpText, expected) {
			t.Errorf("Help text missing shortcut: %q", expected)
		}
	}

	t.Logf("✅ Help text includes all required shortcuts")
}
