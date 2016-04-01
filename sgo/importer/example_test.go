package importer_test

import (
	"os"

	"github.com/tcard/sgo/sgo/ast"
	"github.com/tcard/sgo/sgo/types"

	"github.com/tcard/sgo/sgo/importer"
	"github.com/tcard/sgo/sgo/parser"
	"github.com/tcard/sgo/sgo/printer"
	"github.com/tcard/sgo/sgo/token"
)

func ExampleConvertAST() {
	fset := token.NewFileSet()
	a, _ := parser.ParseFile(fset, "example.sgo", `
	package example

	type LinkedList struct {
		Head	int
		// For SGo: ?*LinkedList
		Tail	*LinkedList
	}
	`, parser.ParseComments)

	info := &types.Info{
		Defs: map[*ast.Ident]types.Object{},
		Uses: map[*ast.Ident]types.Object{},
	}
	cfg := &types.Config{}
	cfg.Check("", fset, []*ast.File{a}, info)

	importer.ConvertAST(a, info, nil)

	printer.Fprint(os.Stdout, fset, a)
	// Output:
	// package example
	//
	// type LinkedList struct {
	// 	Head	int
	// 	// For SGo: ?*LinkedList
	// 	Tail	?*LinkedList
	// }
}
