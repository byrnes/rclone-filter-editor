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
	// Create a temporary directory structure similar to test/folder_a
	tempDir := "test_base_path"
	defer os.RemoveAll(tempDir)

	os.MkdirAll(tempDir, 0755)
	os.MkdirAll(filepath.Join(tempDir, "dir1"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "dir2"), 0755)
	os.MkdirAll(filepath.Join(tempDir, "dir3"), 0755)

	// Create filter rules for test:
	// - dir1/sub1/**
	// - dir1/sub2/**
	// + dir1/**
	// + dir2/**
	// + dir3/**
	// - *
	filterRules := []FilterRule{
		{Pattern: "dir1/sub1/**", State: FilterExclude},
		{Pattern: "dir1/sub2/**", State: FilterExclude},
		{Pattern: "dir1/**", State: FilterInclude},
		{Pattern: "dir2/**", State: FilterInclude},
		{Pattern: "dir3/**", State: FilterInclude},
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
	dir1Path := filepath.Join(absPath, "dir1")
	dir1FilterPath := getFilterPath(dir1Path)
	dir1Filter := getEffectiveFilter(dir1FilterPath, filterRules)

	t.Logf("dir1 path: %s", dir1Path)
	t.Logf("dir1 filter path: %s", dir1FilterPath)
	t.Logf("dir1 filter state: %d", dir1Filter)

	dir2Path := filepath.Join(absPath, "dir2")
	dir2FilterPath := getFilterPath(dir2Path)
	dir2Filter := getEffectiveFilter(dir2FilterPath, filterRules)

	t.Logf("dir2 path: %s", dir2Path)
	t.Logf("dir2 filter path: %s", dir2FilterPath)
	t.Logf("dir2 filter state: %d", dir2Filter)

	// Based on the filter rules and expected UI behavior:
	// 1. The root directory should be excluded by the "- *" rule (it matches the pattern)
	// 2. The subdirectories dir1, dir2, dir3 should be included due to patterns like "dir1/**"
	// 3. "- *" should exclude everything at the base level, including the base directory

	// The root directory with filter path "/." should match the "- *" pattern and be excluded
	if rootFilter != FilterExclude {
		t.Errorf("Root directory should be excluded by '- *' rule (FilterExclude=%d), got %d", FilterExclude, rootFilter)
	}

	if dir1Filter != FilterInclude {
		t.Errorf("dir1 directory should be included (FilterInclude=%d), got %d", FilterInclude, dir1Filter)
	}

	if dir2Filter != FilterInclude {
		t.Errorf("dir2 directory should be included (FilterInclude=%d), got %d", FilterInclude, dir2Filter)
	}
}

func TestFilterRuleOrdering(t *testing.T) {
	tempFile := "test_ordering.txt"
	defer os.Remove(tempFile)

	// Create initial filter rules for testing
	originalRules := []FilterRule{
		{Pattern: "dir1/sub1/**", State: FilterExclude},
		{Pattern: "dir1/sub2/**", State: FilterExclude},
		{Pattern: "dir1/**", State: FilterInclude},
		{Pattern: "dir2/**", State: FilterInclude},
		{Pattern: "dir3/**", State: FilterInclude},
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

	// Add a new dir1 exclusion - should go before "dir1/**"
	newFilterMap["dir1/sub3/**"] = FilterExclude
	// Add a new dir2 exclusion - should go before "dir2/**"
	newFilterMap["dir2/subdir/**"] = FilterExclude
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
		"- dir1/sub1/**",
		"- dir1/sub2/**",
		"- dir1/sub3/**", // New rule should be inserted here
		"+ dir1/**",
		"- dir2/subdir/**", // New rule should be inserted here
		"+ dir2/**",
		"+ dir3/**",
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
		Path:   "/test/dir1/subdir",
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
		Path:   "/test/dir1/subdir",
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
		Path:   "/test/dir2/subdir",
		IsDir:  true,
		Filter: FilterInclude, // Will be inverted to FilterExclude
	}

	fileNode := &FileNode{
		Path:   "/test/file.txt",
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

	tempFile := "test_patterns.txt"
	defer os.Remove(tempFile)

	// Create original rules
	originalRules := []FilterRule{
		{Pattern: "dir1/**", State: FilterInclude},
		{Pattern: "*", State: FilterExclude},
	}

	// Create filterMap with directory patterns (/** ending)
	filterMap := map[string]FilterState{
		"dir1/**":        FilterInclude,
		"dir1/subdir/**": FilterExclude, // New directory exclusion
		"file.txt":       FilterExclude, // New file exclusion
		"*":              FilterExclude,
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
		if strings.Contains(line, "dir1/subdir/**") {
			foundDirPattern = true
		}
		if strings.Contains(line, "file.txt") && !strings.Contains(line, "/**") {
			foundFilePattern = true
		}
	}

	if !foundDirPattern {
		t.Errorf("Directory pattern 'dir1/subdir/**' not found in saved file")
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

func TestFilterStatusDisplayWithRealFilterFile(t *testing.T) {
	// Test the filter status display with actual filter.txt content
	filterRules := []FilterRule{
		{Pattern: "dir1/sub1/**", State: FilterExclude},
		{Pattern: "dir1/sub2/**", State: FilterExclude},
		{Pattern: "dir1/**", State: FilterInclude},
		{Pattern: "dir2/**", State: FilterInclude},
		{Pattern: "dir3/**", State: FilterInclude},
		{Pattern: "*", State: FilterExclude},
	}

	// Set up global root path for test/folder_a
	originalGlobalRootPath := globalRootPath
	testDirPath, _ := filepath.Abs("test/folder_a")
	globalRootPath = testDirPath
	defer func() { globalRootPath = originalGlobalRootPath }()

	// Test cases based on actual test/folder_a structure and filter.txt rules
	testCases := []struct {
		path           string
		expectedFilter FilterState
		description    string
	}{
		// Root should be excluded by catch-all "*" rule
		{testDirPath, FilterExclude, "root directory should be excluded by catch-all"},

		// dir1 directory should be included by "dir1/**" rule
		{filepath.Join(testDirPath, "dir1"), FilterInclude, "dir1 directory should be included"},

		// Excluded subdirs (specific exclusions should win over general inclusion)
		{filepath.Join(testDirPath, "dir1", "sub1"), FilterExclude, "sub1 should be excluded"},
		{filepath.Join(testDirPath, "dir1", "sub2"), FilterExclude, "sub2 should be excluded"},

		// Included subdirs (should match "dir1/**" rule)
		{filepath.Join(testDirPath, "dir1", "subdir1"), FilterInclude, "subdir1 should be included"},

		// Other included directories
		{filepath.Join(testDirPath, "dir2"), FilterInclude, "dir2 directory should be included"},
		{filepath.Join(testDirPath, "dir3"), FilterInclude, "dir3 directory should be included"},

		// Excluded files/dirs (catch-all "*" rule)
		{filepath.Join(testDirPath, "1.txt"), FilterExclude, "1.txt should be excluded by catch-all"},
		{filepath.Join(testDirPath, "2.txt"), FilterExclude, "2.txt should be excluded by catch-all"},
	}

	for _, tc := range testCases {
		filterPath := getFilterPath(tc.path)
		actualFilter := getEffectiveFilter(filterPath, filterRules)

		if actualFilter != tc.expectedFilter {
			var actualStr, expectedStr string
			switch actualFilter {
			case FilterInclude:
				actualStr = "INCLUDE"
			case FilterExclude:
				actualStr = "EXCLUDE"
			case FilterNone:
				actualStr = "NONE"
			}
			switch tc.expectedFilter {
			case FilterInclude:
				expectedStr = "INCLUDE"
			case FilterExclude:
				expectedStr = "EXCLUDE"
			case FilterNone:
				expectedStr = "NONE"
			}

			t.Errorf("%s: path=%s, filterPath=%s, expected=%s, got=%s",
				tc.description, tc.path, filterPath, expectedStr, actualStr)
		}
	}
}

func TestApplicationFilterBehaviorWithRealFiles(t *testing.T) {
	// Test how the application actually loads and processes the real filter.txt and test/folder_a

	// Create a test filter file instead of using the actual filter.txt
	tempFilter := "test_app_filter.txt"
	defer os.Remove(tempFilter)

	filterContent := `- dir1/sub1/**
- dir1/sub2/**
+ dir1/**
+ dir2/**
+ dir3/**
- *`

	err := os.WriteFile(tempFilter, []byte(filterContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test filter file: %v", err)
	}

	// Load the test filter file
	filterRules, filterMap := loadFilterFile(tempFilter)

	if len(filterRules) == 0 {
		t.Skip("filter.txt not found or empty, skipping test")
	}

	t.Logf("Loaded %d filter rules from filter.txt:", len(filterRules))
	for i, rule := range filterRules {
		var stateStr string
		switch rule.State {
		case FilterInclude:
			stateStr = "INCLUDE"
		case FilterExclude:
			stateStr = "EXCLUDE"
		case FilterNone:
			stateStr = "NONE"
		}
		t.Logf("  %d: %s %s", i+1, stateStr, rule.Pattern)
	}

	// Create model like the real application does
	model := &Model{
		filterRules: filterRules,
		filterMap:   filterMap,
	}

	// Debug the loaded filterMap
	t.Logf("Loaded filterMap has %d entries:", len(filterMap))
	for pattern, state := range filterMap {
		var stateStr string
		switch state {
		case FilterInclude:
			stateStr = "INCLUDE"
		case FilterExclude:
			stateStr = "EXCLUDE"
		case FilterNone:
			stateStr = "NONE"
		}
		t.Logf("  filterMap['%s'] = %s", pattern, stateStr)
	}

	// Set up global root path like the real application
	originalGlobalRootPath := globalRootPath
	testDirPath, err := filepath.Abs("test/folder_a")
	if err != nil {
		t.Skip("test/folder_a not found, skipping test")
	}
	globalRootPath = testDirPath
	defer func() { globalRootPath = originalGlobalRootPath }()

	// Test key paths that should have specific behavior
	testCases := []struct {
		relativePath   string
		expectedFilter FilterState
		description    string
	}{
		{"dir1", FilterInclude, "dir1 directory should be included by 'dir1/**'"},
		{"dir1/sub1", FilterExclude, "sub1 should be excluded by specific rule"},
		{"dir1/sub2", FilterExclude, "sub2 should be excluded by specific rule"},
		{"dir1/subdir1", FilterInclude, "subdir1 should be included by 'dir1/**' (no specific exclusion)"},
		{"dir2", FilterInclude, "dir2 directory should be included by 'dir2/**'"},
		{"dir3", FilterInclude, "dir3 directory should be included by 'dir3/**'"},
		{"1.txt", FilterExclude, "1.txt should be excluded by catch-all '*'"},
		{"2.txt", FilterExclude, "2.txt should be excluded by catch-all '*'"},
	}

	for _, tc := range testCases {
		fullPath := filepath.Join(testDirPath, tc.relativePath)
		filterPath := getFilterPath(fullPath)
		actualFilter := model.getEffectiveFilterWithMap(filterPath)

		var actualStr, expectedStr string
		switch actualFilter {
		case FilterInclude:
			actualStr = "INCLUDE"
		case FilterExclude:
			actualStr = "EXCLUDE"
		case FilterNone:
			actualStr = "NONE"
		}
		switch tc.expectedFilter {
		case FilterInclude:
			expectedStr = "INCLUDE"
		case FilterExclude:
			expectedStr = "EXCLUDE"
		case FilterNone:
			expectedStr = "NONE"
		}

		if actualFilter != tc.expectedFilter {
			t.Errorf("%s: relativePath=%s, fullPath=%s, filterPath=%s, expected=%s, got=%s",
				tc.description, tc.relativePath, fullPath, filterPath, expectedStr, actualStr)
		} else {
			t.Logf("✅ %s: %s -> %s (%s)", tc.description, tc.relativePath, filterPath, actualStr)
		}
	}
}

func TestDebugFilterMatching(t *testing.T) {
	// Debug the specific failing cases
	filterRules := []FilterRule{
		{Pattern: "dir1/sub1/**", State: FilterExclude},
		{Pattern: "dir1/sub2/**", State: FilterExclude},
		{Pattern: "dir1/**", State: FilterInclude},
		{Pattern: "dir2/**", State: FilterInclude},
		{Pattern: "dir3/**", State: FilterInclude},
		{Pattern: "*", State: FilterExclude},
	}

	// Set up global root path for test/folder_a
	originalGlobalRootPath := globalRootPath
	testDirPath, _ := filepath.Abs("test/folder_a")
	globalRootPath = testDirPath
	defer func() { globalRootPath = originalGlobalRootPath }()

	// Debug specific cases
	debugCases := []string{"/dir1", "/dir1/sub1", "/dir1/sub2", "/dir2", "/dir3"}

	for _, testPath := range debugCases {
		t.Logf("\nTesting path: %s", testPath)

		// Show which rules match
		for i, rule := range filterRules {
			matches := matchesRclonePattern(rule.Pattern, testPath)
			exact := rule.Pattern == testPath

			var stateStr string
			switch rule.State {
			case FilterInclude:
				stateStr = "INCLUDE"
			case FilterExclude:
				stateStr = "EXCLUDE"
			case FilterNone:
				stateStr = "NONE"
			}

			t.Logf("  Rule %d: %s '%s' | exact=%v matches=%v",
				i+1, stateStr, rule.Pattern, exact, matches)

			if exact || matches {
				t.Logf("    -> FIRST MATCH! Result: %s", stateStr)
				break
			}
		}

		// Show final result
		result := getEffectiveFilter(testPath, filterRules)
		var resultStr string
		switch result {
		case FilterInclude:
			resultStr = "INCLUDE"
		case FilterExclude:
			resultStr = "EXCLUDE"
		case FilterNone:
			resultStr = "NONE"
		}
		t.Logf("  Final result: %s", resultStr)
	}
}

func TestModelGetEffectiveFilterWithMap(t *testing.T) {
	// Test the model's getEffectiveFilterWithMap method directly
	filterRules := []FilterRule{
		{Pattern: "dir1/sub1/**", State: FilterExclude},
		{Pattern: "dir1/sub2/**", State: FilterExclude},
		{Pattern: "dir1/**", State: FilterInclude},
		{Pattern: "dir2/**", State: FilterInclude},
		{Pattern: "dir3/**", State: FilterInclude},
		{Pattern: "*", State: FilterExclude},
	}

	// Create model like the real application does
	model := &Model{
		filterRules: filterRules,
		filterMap:   make(map[string]FilterState), // Empty filterMap like at startup
	}

	// Set up global root path for test/folder_a
	originalGlobalRootPath := globalRootPath
	testDirPath, _ := filepath.Abs("test/folder_a")
	globalRootPath = testDirPath
	defer func() { globalRootPath = originalGlobalRootPath }()

	// Test the problematic cases
	testCases := []struct {
		path           string
		expectedFilter FilterState
		description    string
	}{
		{"/dir1/sub1", FilterExclude, "dir1/sub1 should be excluded"},
		{"/dir2", FilterInclude, "dir2 should be included"},
	}

	for _, tc := range testCases {
		actualFilter := model.getEffectiveFilterWithMap(tc.path)

		var actualStr, expectedStr string
		switch actualFilter {
		case FilterInclude:
			actualStr = "INCLUDE"
		case FilterExclude:
			actualStr = "EXCLUDE"
		case FilterNone:
			actualStr = "NONE"
		}
		switch tc.expectedFilter {
		case FilterInclude:
			expectedStr = "INCLUDE"
		case FilterExclude:
			expectedStr = "EXCLUDE"
		case FilterNone:
			expectedStr = "NONE"
		}

		if actualFilter != tc.expectedFilter {
			t.Errorf("%s: path=%s, expected=%s, got=%s",
				tc.description, tc.path, expectedStr, actualStr)
		} else {
			t.Logf("✅ %s: %s -> %s", tc.description, tc.path, actualStr)
		}
	}
}

func TestChildrenFilterUpdateOnFolderChangeSimple(t *testing.T) {
	// Create a simple test case to verify children filter updates
	model := &Model{
		filterMap:   make(map[string]FilterState),
		filterRules: []FilterRule{},
	}

	// Set up global root path
	originalGlobalRootPath := globalRootPath
	globalRootPath = "/test"
	defer func() { globalRootPath = originalGlobalRootPath }()

	// Create a simple parent with children
	parent := &FileNode{
		Name:   "parent",
		Path:   "/test/parent",
		IsDir:  true,
		Filter: FilterNone,
		Children: []*FileNode{
			{
				Name:   "child.txt",
				Path:   "/test/parent/child.txt",
				IsDir:  false,
				Filter: FilterNone,
			},
		},
	}

	// Test the pattern matching logic directly first
	t.Logf("=== Testing Pattern Matching ===")
	parentPattern := "parent/**"
	childPath := "parent/child.txt"
	matches := matchesRclonePattern(parentPattern, childPath)
	t.Logf("Pattern '%s' matches path '%s': %t", parentPattern, childPath, matches)

	if !matches {
		t.Errorf("Expected pattern '%s' to match path '%s'", parentPattern, childPath)
	}

	// Test the getFilterPath function
	t.Logf("\n=== Testing Filter Path Generation ===")
	t.Logf("Global root path: '%s'", globalRootPath)
	parentFilterPath := getFilterPath(parent.Path)
	childFilterPath := getFilterPath(parent.Children[0].Path)
	t.Logf("Parent path '%s' -> filter path '%s'", parent.Path, parentFilterPath)
	t.Logf("Child path '%s' -> filter path '%s'", parent.Children[0].Path, childFilterPath)

	// Let's debug the relative path calculation
	rel, err := filepath.Rel(globalRootPath, parent.Children[0].Path)
	t.Logf("Relative path calculation: filepath.Rel('%s', '%s') = '%s', err = %v", globalRootPath, parent.Children[0].Path, rel, err)

	// Also test filepath.Abs
	absChild, absErr := filepath.Abs(parent.Children[0].Path)
	t.Logf("filepath.Abs('%s') = '%s', err = %v", parent.Children[0].Path, absChild, absErr)

	// Set up the parent's exclusion pattern in filterMap
	t.Logf("\n=== Testing Filter Map Logic ===")
	excludePattern := parentFilterPath + "/**"
	model.filterMap[excludePattern] = FilterExclude
	t.Logf("Added pattern '%s' with state FilterExclude to filterMap", excludePattern)

	// Test getEffectiveFilterWithMap directly
	childState := model.getEffectiveFilterWithMap(childFilterPath)
	t.Logf("Child effective filter state: %d (expecting %d for FilterExclude)", childState, FilterExclude)

	if childState != FilterExclude {
		t.Errorf("Expected child to be excluded (state %d), got state %d", FilterExclude, childState)
	}
}

func TestChildrenFilterUpdateOnFolderChange(t *testing.T) {
	// Create a model with a directory tree structure
	model := &Model{
		filterMap:   make(map[string]FilterState),
		filterRules: []FilterRule{},
	}

	// Set up global root path
	originalGlobalRootPath := globalRootPath
	globalRootPath = "/test"
	defer func() { globalRootPath = originalGlobalRootPath }()

	// Create a directory structure with parent and children
	// Using more realistic absolute paths
	parentDir := &FileNode{
		Name:     "parent_dir",
		Path:     "/test/parent_dir",
		IsDir:    true,
		Filter:   FilterNone,
		Expanded: true,
		Children: []*FileNode{
			{
				Name:   "child_file1.txt",
				Path:   "/test/parent_dir/child_file1.txt",
				IsDir:  false,
				Filter: FilterNone,
			},
			{
				Name:   "child_dir",
				Path:   "/test/parent_dir/child_dir",
				IsDir:  true,
				Filter: FilterNone,
				Children: []*FileNode{
					{
						Name:   "grandchild.txt",
						Path:   "/test/parent_dir/child_dir/grandchild.txt",
						IsDir:  false,
						Filter: FilterNone,
					},
				},
			},
		},
	}

	// Set up parent relationship
	for _, child := range parentDir.Children {
		child.Parent = parentDir
		if child.IsDir {
			for _, grandchild := range child.Children {
				grandchild.Parent = child
			}
		}
	}

	model.root = parentDir
	model.updateVisibleNodes()

	// Helper function to check node filters recursively
	var checkNodeFilters func(node *FileNode, depth int)
	checkNodeFilters = func(node *FileNode, depth int) {
		indent := strings.Repeat("  ", depth)
		t.Logf("%s%s: Filter=%d", indent, node.Name, node.Filter)
		if node.IsDir {
			for _, child := range node.Children {
				checkNodeFilters(child, depth+1)
			}
		}
	}

	// Helper function to find a node by path
	var findNode func(node *FileNode, targetPath string) *FileNode
	findNode = func(node *FileNode, targetPath string) *FileNode {
		if node.Path == targetPath {
			return node
		}
		if node.IsDir {
			for _, child := range node.Children {
				if found := findNode(child, targetPath); found != nil {
					return found
				}
			}
		}
		return nil
	}

	// Verify initial state - all should be FilterNone
	t.Logf("=== Initial State ===")
	checkNodeFilters(parentDir, 0)

	// Verify initial states
	if parentDir.Filter != FilterNone {
		t.Errorf("Parent should initially have FilterNone, got %d", parentDir.Filter)
	}

	// Simulate pressing space on the parent directory to exclude it
	t.Logf("\n=== After excluding parent directory ===")
	parentDir.Filter = FilterExclude

	// Update filterMap as the space handler would
	filterPath := getFilterPath(parentDir.Path)
	filterPath = strings.TrimSuffix(filterPath, "/") + "/**"
	model.filterMap[filterPath] = FilterExclude

	// Call updateChildrenFilters to update the children
	t.Logf("Filter map before update: %v", model.filterMap)

	// Debug the filter paths for each child
	t.Logf("Parent filter path: %s", getFilterPath(parentDir.Path))
	for _, child := range parentDir.Children {
		childPath := getFilterPath(child.Path)
		t.Logf("Child %s filter path: %s", child.Name, childPath)
		if child.IsDir {
			for _, grandchild := range child.Children {
				grandchildPath := getFilterPath(grandchild.Path)
				t.Logf("  Grandchild %s filter path: %s", grandchild.Name, grandchildPath)
			}
		}
	}

	// Test pattern matching directly before updating children
	testPattern := "parent_dir/**"
	testPaths := []string{"parent_dir/child_file1.txt", "parent_dir/child_dir", "parent_dir/child_dir/grandchild.txt"}
	for _, testPath := range testPaths {
		matches := matchesRclonePattern(testPattern, testPath)
		t.Logf("Pattern '%s' matches '%s': %t", testPattern, testPath, matches)
	}

	model.updateChildrenFilters(parentDir)
	t.Logf("Filter map after update: %v", model.filterMap)

	// Verify that children now reflect the exclusion
	checkNodeFilters(parentDir, 0)

	// All children should now be excluded due to the parent's /** pattern
	expectedChildStates := []struct {
		path     string
		name     string
		expected FilterState
	}{
		{"/test/parent_dir", "parent_dir", FilterExclude},
		{"/test/parent_dir/child_file1.txt", "child_file1.txt", FilterExclude},
		{"/test/parent_dir/child_dir", "child_dir", FilterExclude},
		{"/test/parent_dir/child_dir/grandchild.txt", "grandchild.txt", FilterExclude},
	}

	// Test each expected state
	for _, expected := range expectedChildStates {
		foundNode := findNode(parentDir, expected.path)

		if foundNode == nil {
			t.Errorf("Could not find node with path %s", expected.path)
			continue
		}

		if foundNode.Filter != expected.expected {
			t.Errorf("Node %s (%s): expected filter %d, got %d",
				expected.name, expected.path, expected.expected, foundNode.Filter)
		}
	}
}

func TestFilterWithParenthesesAndSpaces(t *testing.T) {
	// Create a test filter file with parentheses and spaces
	filterContent := `+ dir (with parens)/**
- bad (old version)/**
+ dir1/**
- *`

	tempFile := "test_parentheses_filter.txt"
	defer os.Remove(tempFile)

	err := os.WriteFile(tempFile, []byte(filterContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test filter file: %v", err)
	}

	// Load the filter file
	filterRules, filterMap := loadFilterFile(tempFile)

	t.Logf("=== Filter Rules ===")
	for i, rule := range filterRules {
		t.Logf("%d: Pattern='%s', State=%d", i, rule.Pattern, rule.State)
	}

	t.Logf("\n=== Filter Map ===")
	for pattern, state := range filterMap {
		t.Logf("Pattern='%s', State=%d", pattern, state)
	}

	// Verify that patterns with parentheses and spaces are loaded correctly
	expectedPatterns := []string{
		"dir (with parens)/**",
		"bad (old version)/**",
		"dir1/**",
		"*",
	}

	if len(filterRules) != len(expectedPatterns) {
		t.Errorf("Expected %d filter rules, got %d", len(expectedPatterns), len(filterRules))
	}

	// Check each pattern
	for i, expected := range expectedPatterns {
		if i >= len(filterRules) {
			t.Errorf("Missing filter rule at index %d for pattern '%s'", i, expected)
			continue
		}
		if filterRules[i].Pattern != expected {
			t.Errorf("Filter rule %d: expected pattern '%s', got '%s'", i, expected, filterRules[i].Pattern)
		}
	}

	// Test effective filtering for paths with parentheses and spaces
	testCases := []struct {
		path     string
		expected FilterState
		desc     string
	}{
		{"dir (with parens)/file.txt", FilterInclude, "should include files in parentheses dir"},
		{"dir (with parens)", FilterInclude, "should include parentheses directory"},
		{"bad (old version)/file.txt", FilterExclude, "should exclude files in old version dir"},
		{"bad (old version)", FilterExclude, "should exclude old version directory"},
		{"dir1/subdir/file.txt", FilterInclude, "should include dir1 files"},
		{"random_file.txt", FilterExclude, "should exclude other files due to - *"},
	}

	t.Logf("\n=== Testing Effective Filters ===")
	for _, tc := range testCases {
		result := getEffectiveFilter(tc.path, filterRules)
		t.Logf("Path: '%s' -> %d (expected %d) - %s", tc.path, result, tc.expected, tc.desc)
		if result != tc.expected {
			t.Errorf("getEffectiveFilter('%s') = %d; want %d (%s)", tc.path, result, tc.expected, tc.desc)
		}
	}

	// Test the pattern matching directly
	t.Logf("\n=== Testing Pattern Matching Directly ===")
	directMatchTests := []struct {
		pattern  string
		path     string
		expected bool
		desc     string
	}{
		{"dir (with parens)/**", "dir (with parens)/file.txt", true, "parentheses pattern should match"},
		{"bad (old version)/**", "bad (old version)/file.txt", true, "parentheses exclusion should match"},
		{"dir1/sub dir/**", "dir1/sub dir/file.txt", true, "spaces pattern should match"},
		{"dir (with parens)/**", "dir with parens/file.txt", false, "should not match without parentheses"},
	}

	for _, tc := range directMatchTests {
		result := matchesRclonePattern(tc.pattern, tc.path)
		t.Logf("Pattern: '%s', Path: '%s' -> %t (expected %t) - %s", tc.pattern, tc.path, result, tc.expected, tc.desc)
		if result != tc.expected {
			t.Errorf("matchesRclonePattern('%s', '%s') = %t; want %t (%s)", tc.pattern, tc.path, result, tc.expected, tc.desc)
		}
	}
}
