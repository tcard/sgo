// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package importer provides access to export data importers.
package importer

import (
	"go/build"
	"os"
	"path/filepath"

	"github.com/tcard/sgo/sgo/ast"
	"github.com/tcard/sgo/sgo/parser"
	"github.com/tcard/sgo/sgo/token"
	"github.com/tcard/sgo/sgo/types"
)

// Default returns a types.Importer that imports from Go source code and
// transforms to SGo.
//
// Conversion is performed by passing the AST through ConvertAST.
func Default() types.Importer {
	return &importer{}
}

type importer struct{}

func (imp *importer) Import(path string) (*types.Package, error) {
	buildPkg, err := build.Import(path, "", build.ImportComment)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()

	var files []*ast.File
	for _, name := range buildPkg.GoFiles {
		path := filepath.Join(buildPkg.Dir, name)
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		a, err := parser.ParseFile(fset, name, f, parser.ParseComments)
		f.Close()
		if err != nil {
			return nil, err
		}
		files = append(files, a)
	}

	// 1. Typecheck without converting anything; ConvertAST needs to know
	//    which idents are types to perform the default conversions.

	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	cfg := &types.Config{
		IgnoreFuncBodies:        true,
		IgnoreTopLevelVarValues: true,
		Importer:                imp,
	}
	pkg, err := cfg.Check(path, fset, files, info)
	if err != nil {
		return nil, err
	}

	// 2. Convert AST, now using the doc comment annotations and converting
	//    everything that hasn't been converted explicitly by then with the
	//    default conversion (wrapping in optionals).

	for _, f := range files {
		ConvertAST(f, info)
	}

	// 3. Typecheck converted AST.

	pkg, err = cfg.Check(path, fset, files, &types.Info{})
	if err != nil {
		return nil, err
	}

	return pkg, nil
}
