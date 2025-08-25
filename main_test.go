package main

import (
	"os"
	"path/filepath"
	"testing"
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

	err := saveFilterFile(tempFile, originalMap)
	if err != nil {
		t.Fatalf("Failed to save filter file: %v", err)
	}

	loadedMap := loadFilterFile(tempFile)

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
	filterMap := map[string]FilterState{
		"/exact/file.txt":     FilterInclude,
		"*.log":               FilterExclude,
		"**/*.test":           FilterExclude,
		"src/**/*.go":         FilterInclude,
		"/temp/**":            FilterExclude,
		"{config,settings}.*": FilterInclude,
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
		result := getEffectiveFilter(tt.path, filterMap)
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
	filterMap := loadFilterFile(tempFile)

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

	err := saveFilterFile(tempFile, filterMap)
	if err != nil {
		t.Fatalf("Failed to save filter file: %v", err)
	}

	// Load it back and verify
	loadedMap := loadFilterFile(tempFile)

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
