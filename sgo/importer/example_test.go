package importer_test

import (
	"os"

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

	importer.ConvertAST(a)

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
