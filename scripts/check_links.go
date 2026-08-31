//go:build ignore

// check_links.go scans the active documentation (repo-root *.md and docs/**,
// excluding the reports/archive historical snapshots and git internals) and
// verifies every relative markdown link target exists. It exits non-zero on
// any broken link so CI can fail on stale references.
//
// Usage: go run scripts/check_links.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var linkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil // file or directory (dir links are valid on GitHub)
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "getwd:", err)
		os.Exit(1)
	}

	var broken []string
	checked := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "archive", "logs", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Only check repo-root markdown and docs/ (skip deploy/ etc.).
		if strings.ContainsRune(rel, os.PathSeparator) && !strings.HasPrefix(rel, "docs"+string(os.PathSeparator)) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		for _, m := range linkRe.FindAllStringSubmatch(string(content), -1) {
			target := m[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
				strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") ||
				strings.HasPrefix(target, "data:") {
				continue
			}
			target = strings.SplitN(target, "#", 2)[0] // strip anchor
			if target == "" {
				continue
			}
			c1 := filepath.Join(dir, filepath.FromSlash(target))
			c2 := filepath.Join(root, filepath.FromSlash(target))
			if !pathExists(c1) && !pathExists(c2) {
				broken = append(broken, fmt.Sprintf("%s => %s", rel, m[1]))
			}
			checked++
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "walk:", err)
		os.Exit(1)
	}

	if len(broken) > 0 {
		fmt.Printf("checked %d links, found %d broken:\n", checked, len(broken))
		for _, b := range broken {
			fmt.Println("  " + b)
		}
		os.Exit(1)
	}
	fmt.Printf("OK: %d relative markdown links resolve.\n", checked)
}
