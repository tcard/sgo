package importer

import (
	goast "go/ast"
	gotypes "go/types"
)

type change struct {
	isOptional   bool
	entangledPos int
}

// An annotation maps go/type values from a declaration from a *Package (*Var,
// *Tuple, etc.) to changes to be performed when importing the package into
// an SGo program.
type annotation map[interface{}]change

// stdAnnotations returns the predefined annotations for a Go standard library
// package, or nil if undefined.
func stdAnnotations(pkg *gotypes.Package, path string) map[string]annotation {
	return nil
}

func annotationsFromAST(pkg *gotypes.Package, files []*goast.File) map[string]annotation {
	return map[string]annotation{}
}
