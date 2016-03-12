// Package importpaths exposes part of the functionality in the go tool for
// referring to import paths, typically from the command line arguments.
package importpaths

import (
	"fmt"
	"go/build"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// ImportPathsNoDotExpansion returns the import paths to use for the given
// command line, but it does no ... expansion.
func ImportPathsNoDotExpansion(args []string) []string {
	if len(args) == 0 {
		return []string{"."}
	}
	var out []string
	for _, a := range args {
		// Arguments are supposed to be import paths, but
		// as a courtesy to Windows developers, rewrite \ to /
		// in command-line arguments.  Handles .\... and so on.
		if filepath.Separator == '\\' {
			a = strings.Replace(a, `\`, `/`, -1)
		}

		// Put argument in canonical form, but preserve leading ./.
		if strings.HasPrefix(a, "./") {
			a = "./" + path.Clean(a)
			if a == "./." {
				a = "."
			}
		} else {
			a = path.Clean(a)
		}
		out = append(out, a)
	}
	return out
}

// ImportPaths returns the import paths to use for the given command line.
func ImportPaths(args []string) ([]string, []error) {
	args = ImportPathsNoDotExpansion(args)
	var out []string
	var errs []error
	for _, a := range args {
		if strings.Contains(a, "...") {
			if build.IsLocalImport(a) {
				all, err := AllPackagesInFS(a)
				out = append(out, all...)
				if err != nil {
					errs = append(errs, err)
				}
			} else {
				all, err := AllPackages(a)
				out = append(out, all...)
				if err != nil {
					errs = append(errs, err)
				}
			}
			continue
		}
		out = append(out, a)
	}
	return out, errs
}

// treeCanMatchPattern(pattern)(name) reports whether
// name or children of name can possibly match pattern.
// Pattern is the same limited glob accepted by matchPattern.
func treeCanMatchPattern(pattern string) func(name string) bool {
	wildCard := false
	if i := strings.Index(pattern, "..."); i >= 0 {
		wildCard = true
		pattern = pattern[:i]
	}
	return func(name string) bool {
		return len(name) <= len(pattern) && hasPathPrefix(pattern, name) ||
			wildCard && strings.HasPrefix(name, pattern)
	}
}

// AllPackages returns all the packages that can be found
// under the $GOPATH directories matching pattern.
// The pattern is either "all" (all packages) or a path including "...".
func AllPackages(pattern string) ([]string, error) {
	pkgs := matchPackages(pattern)
	var err error
	if len(pkgs) == 0 {
		err = fmt.Errorf("warning: %q matched no packages\n", pattern)
	}
	return pkgs, err
}

func matchPackages(pattern string) []string {
	match := matchPattern(pattern)
	treeCanMatch := treeCanMatchPattern(pattern)

	have := map[string]bool{}
	var pkgs []string

	for _, src := range build.Default.SrcDirs() {
		src = filepath.Clean(src) + string(filepath.Separator)
		root := src
		filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
			if err != nil || !fi.IsDir() || path == src {
				return nil
			}

			// Avoid .foo, _foo, and testdata directory trees.
			_, elem := filepath.Split(path)
			if strings.HasPrefix(elem, ".") || strings.HasPrefix(elem, "_") || elem == "testdata" {
				return filepath.SkipDir
			}

			name := filepath.ToSlash(path[len(src):])
			if pattern == "std" && (!IsStandardImportPath(name) || name == "cmd") {
				// The name "std" is only the standard library.
				// If the name is cmd, it's the root of the command tree.
				return filepath.SkipDir
			}
			if !treeCanMatch(name) {
				return filepath.SkipDir
			}
			if have[name] {
				return nil
			}
			have[name] = true
			if !match(name) {
				return nil
			}
			pkgs = append(pkgs, name)
			return nil
		})
	}
	return pkgs
}

// AllPackagesInFS is like AllPackages but is passed a pattern
// beginning ./ or ../, meaning it should scan the tree rooted
// at the given directory.  There are ... in the pattern too.
func AllPackagesInFS(pattern string) ([]string, error) {
	pkgs := matchPackagesInFS(pattern)
	var err error
	if len(pkgs) == 0 {
		err = fmt.Errorf("warning: %q matched no packages\n", pattern)
	}
	return pkgs, err
}

func matchPackagesInFS(pattern string) []string {
	// Find directory to begin the scan.
	// Could be smarter but this one optimization
	// is enough for now, since ... is usually at the
	// end of a path.
	i := strings.Index(pattern, "...")
	dir, _ := path.Split(pattern[:i])

	// pattern begins with ./ or ../.
	// path.Clean will discard the ./ but not the ../.
	// We need to preserve the ./ for pattern matching
	// and in the returned import paths.
	prefix := ""
	if strings.HasPrefix(pattern, "./") {
		prefix = "./"
	}
	match := matchPattern(pattern)

	var pkgs []string
	filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() {
			return nil
		}
		if path == dir {
			// filepath.Walk starts at dir and recurses. For the recursive case,
			// the path is the result of filepath.Join, which calls filepath.Clean.
			// The initial case is not Cleaned, though, so we do this explicitly.
			//
			// This converts a path like "./io/" to "io". Without this step, running
			// "cd $GOROOT/src; go list ./io/..." would incorrectly skip the io
			// package, because prepending the prefix "./" to the unclean path would
			// result in "././io", and match("././io") returns false.
			path = filepath.Clean(path)
		}

		// Avoid .foo, _foo, and testdata directory trees, but do not avoid "." or "..".
		_, elem := filepath.Split(path)
		dot := strings.HasPrefix(elem, ".") && elem != "." && elem != ".."
		if dot || strings.HasPrefix(elem, "_") || elem == "testdata" {
			return filepath.SkipDir
		}

		name := prefix + filepath.ToSlash(path)
		if !match(name) {
			return nil
		}

		pkgs = append(pkgs, name)
		return nil
	})
	return pkgs
}

// matchPattern(pattern)(name) reports whether
// name matches pattern.  Pattern is a limited glob
// pattern in which '...' means 'any string' and there
// is no other special syntax.
func matchPattern(pattern string) func(name string) bool {
	re := regexp.QuoteMeta(pattern)
	re = strings.Replace(re, `\.\.\.`, `.*`, -1)
	// Special case: foo/... matches foo too.
	if strings.HasSuffix(re, `/.*`) {
		re = re[:len(re)-len(`/.*`)] + `(/.*)?`
	}
	reg := regexp.MustCompile(`^` + re + `$`)
	return func(name string) bool {
		return reg.MatchString(name)
	}
}

// hasPathPrefix reports whether the path s begins with the
// elements in prefix.
func hasPathPrefix(s, prefix string) bool {
	switch {
	default:
		return false
	case len(s) == len(prefix):
		return s == prefix
	case len(s) > len(prefix):
		if prefix != "" && prefix[len(prefix)-1] == '/' {
			return strings.HasPrefix(s, prefix)
		}
		return s[len(prefix)] == '/' && s[:len(prefix)] == prefix
	}
}

// IsStandardImportPath reports whether $GOROOT/src/path should be considered
// part of the standard distribution. For historical reasons we allow people to add
// their own code to $GOROOT instead of using $GOPATH, but we assume that
// code will start with a domain name (dot in the first element).
func IsStandardImportPath(path string) bool {
	i := strings.Index(path, "/")
	if i < 0 {
		i = len(path)
	}
	elem := path[:i]
	return !strings.Contains(elem, ".")
}
