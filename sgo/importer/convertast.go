package importer

import (
	"reflect"
	"strings"

	"github.com/tcard/sgo/sgo/ast"
	"github.com/tcard/sgo/sgo/parser"
)

// ConvertAST transforms an imported Go AST applying type annotations found in
// doc comments.
//
// It looks up doc comment lines of the form 'For SGO: <annotation>',
// where <annotation> is a type expression. If found, the annotation replaces
// the type of the documented declaration or field.
//
// TODO: This takes a SGo *ast.File, when it should take a "go/ast".*File. This
// is a hack that takes advantage of the fact that every Go AST is also a valid
// SGo AST; this way, we don't have to convert from Go to SGo types, which is
// a pain.
func ConvertAST(a *ast.File) {
	convertAST(a, nil)
}

func convertAST(node ast.Node, replace func(e ast.Expr)) {
	maybeReplace(node, replace)

	switch n := node.(type) {
	case *ast.Field:
		convertAST(n.Type, nil)

	case *ast.FieldList:
		for _, f := range n.List {
			convertAST(f, func(e ast.Expr) { f.Type = e })
		}

	case *ast.StarExpr:
		convertAST(n.X, nil)

	// Types
	case *ast.ArrayType:
		convertAST(n.Elt, nil)

	case *ast.StructType:
		convertAST(n.Fields, nil)

	case *ast.FuncType:
		if n.Params != nil {
			convertAST(n.Params, nil)
		}
		if n.Results != nil {
			convertAST(n.Results, nil)
		}

	case *ast.InterfaceType:
		convertAST(n.Methods, nil)

	case *ast.MapType:
		convertAST(n.Key, nil)
		convertAST(n.Value, nil)

	case *ast.ChanType:
		convertAST(n.Value, nil)

	// Declarations
	case *ast.ValueSpec:
		if n.Type != nil {
			convertAST(n.Type, nil)
		}

	case *ast.TypeSpec:
		convertAST(n.Type, nil)

	case *ast.GenDecl:
		for _, s := range n.Specs {
			switch s := s.(type) {
			case *ast.ImportSpec:
				convertAST(s, nil)
			case *ast.ValueSpec:
				convertAST(s, func(e ast.Expr) { s.Type = e })
			case *ast.TypeSpec:
				convertAST(s, func(e ast.Expr) { s.Type = e })

			}
		}

	case *ast.FuncDecl:
		if n.Recv != nil {
			convertAST(n.Recv, nil)
		}
		convertAST(n.Type, nil)

	case *ast.File:
		for _, d := range n.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				convertAST(d, nil)
			case *ast.FuncDecl:
				convertAST(d, func(e ast.Expr) {
					if e, ok := e.(*ast.FuncType); ok {
						d.Type = e
					}
				})
			}
		}
	}
}

func maybeReplace(node ast.Node, replace func(e ast.Expr)) {
	n := reflect.ValueOf(node)

	if n.Elem().Type().Kind() != reflect.Struct {
		return
	}

	doc := n.Elem().FieldByName("Doc")
	if !doc.IsValid() {
		return
	}

	cg := doc.Interface().(*ast.CommentGroup)
	if cg == nil {
		return
	}

	for _, l := range cg.List {
		s := l.Text
		s = strings.TrimPrefix(s, "//")
		s = strings.TrimPrefix(s, "/*")
		s = strings.TrimSpace(s)

		if !strings.HasPrefix(s, "For SGo: ") {
			continue
		}

		ann := s[len("For SGo: "):]
		e, err := parser.ParseExpr(ann)
		if err != nil {
			return
		}

		replace(e)
		break
	}
}
