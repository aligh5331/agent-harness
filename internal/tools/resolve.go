package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveScoped resolves a relative or absolute path against the project root
// and verifies it does not escape the root via ".." traversal or symlink escape.
//
// Resolution order:
//  1. Join path against root.
//  2. Try to resolve all symlinks in the joined path (filepath.EvalSymlinks).
//     If the full path exists, the resolved value is used for the prefix check.
//  3. If the path does not exist yet (e.g. create_file), walk up the directory
//     tree to find the longest existing ancestor, resolve symlinks on that,
//     then re-join the non-existent remainder.
//  4. Check the resolved path is within the project root.
//
// Symlinks are resolved BEFORE the prefix check. This prevents a symlink inside
// the project root that points outside from bypassing scoping.
func resolveScoped(root, path string) (string, error) {
	joined := filepath.Join(root, path)

	// First, try full EvalSymlinks — works if path already exists.
	resolved, err := filepath.EvalSymlinks(joined)
	if err == nil {
		return checkPrefix(root, resolved)
	}

	// Path does not exist (e.g. create_file for a new file).
	// Walk up the path to find the longest existing prefix, resolve that,
	// then re-attach the non-existent tail.
	resolved, remainder, err := resolvePartial(joined)
	if err != nil {
		return "", fmt.Errorf("path %q cannot be resolved against root: %w", path, err)
	}

	abs := filepath.Join(resolved, remainder)
	return checkPrefix(root, abs)
}

// checkPrefix verifies abspath is within (or equal to) root, with symlinks resolved.
// The abspath argument must already have its symlinks resolved (via resolveScoped
// or resolvePartial). Root's symlinks are also resolved here so both sides of the
// prefix comparison are on the same resolution basis.
func checkPrefix(root, abspath string) (string, error) {
	// Resolve root's symlinks so the comparison basis matches abspath,
	// which has already been symlink-resolved by resolveScoped/resolvePartial.
	evalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		// Fallback: if root doesn't exist as a real path, use the original
		// value. The Abs call below will error if root is truly unusable,
		// but for a valid project root on disk EvalSymlinks always succeeds.
		evalRoot = root
	}
	absRoot, err := filepath.Abs(evalRoot)
	if err != nil {
		return "", fmt.Errorf("resolve root %q: %w", root, err)
	}
	absRoot = filepath.Clean(absRoot)

	if abspath == absRoot {
		return abspath, nil
	}

	// Ensure the resolved path starts with root + separator.
	prefix := absRoot + string(os.PathSeparator)
	if !strings.HasPrefix(abspath, prefix) {
		return "", &PathEscapeError{Path: abspath, Root: absRoot}
	}

	return abspath, nil
}

// resolvePartial walks up from path to find the longest existing ancestor,
// resolves symlinks on that ancestor, and returns the resolved ancestor plus
// the remaining non-existent path components.
func resolvePartial(path string) (resolved, remainder string, err error) {
	candidate := path
	for {
		r, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			// Found an existing ancestor. Return the resolved path plus whatever
			// remains of the original path after this ancestor.
			tail := strings.TrimPrefix(path, candidate)
			return r, tail, nil
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			// We've reached the filesystem root without finding an existing ancestor.
			// Fall back to the cleaned joined path.
			return filepath.Clean(path), "", nil
		}
		candidate = parent
	}
}
