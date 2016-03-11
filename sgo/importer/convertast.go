package importer

import (
	"reflect"
	"strings"

	"github.com/tcard/sgo/sgo/ast"
	"github.com/tcard/sgo/sgo/parser"
	"github.com/tcard/sgo/sgo/types"
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
func ConvertAST(a *ast.File, info *types.Info) {
	c := astConverter{info: info}
	c.convertAST(a, nil)
}

type astConverter struct {
	info      *types.Info
	converted map[interface{}]struct{}
}

func (c *astConverter) convertAST(node ast.Node, replace func(e ast.Expr)) {
	if replaced := c.maybeReplace(node, replace); replaced {
		return
	}

	switch n := node.(type) {
	case *ast.Field:
		c.convertAST(n.Type, func(e ast.Expr) { n.Type = e })

	case *ast.FieldList:
		for _, f := range n.List {
			c.convertAST(f, nil)
		}

	case *ast.StarExpr:
		replace(&ast.OptionalType{Elt: n})
		c.convertAST(n.X, func(e ast.Expr) { n.X = e })

	case *ast.Ident:
		u, ok := c.info.Uses[n]
		if !ok {
			break
		}
		tn, ok := u.(*types.TypeName)
		if !ok {
			break
		}
		if replace != nil && types.IsInterface(tn.Type()) {
			replace(&ast.OptionalType{Elt: n})
		}

	// Types
	case *ast.ArrayType:
		c.convertAST(n.Elt, func(e ast.Expr) { n.Elt = e })

	case *ast.StructType:
		c.convertAST(n.Fields, nil)

	case *ast.FuncType:
		if replace != nil {
			replace(&ast.OptionalType{Elt: n})
		}
		if n.Params != nil {
			c.convertAST(n.Params, nil)
		}
		if n.Results != nil {
			c.convertAST(n.Results, nil)
		}

	case *ast.InterfaceType:
		if replace != nil {
			replace(&ast.OptionalType{Elt: n})
		}
		for _, f := range n.Methods.List {
			c.convertAST(f.Type, nil)
		}

	case *ast.MapType:
		replace(&ast.OptionalType{Elt: n})
		c.convertAST(n.Key, func(e ast.Expr) { n.Key = e })
		c.convertAST(n.Value, func(e ast.Expr) { n.Value = e })

	case *ast.ChanType:
		replace(&ast.OptionalType{Elt: n})
		c.convertAST(n.Value, func(e ast.Expr) { n.Value = e })

	// Declarations
	case *ast.ValueSpec:
		if n.Type != nil {
			c.convertAST(n.Type, func(e ast.Expr) { n.Type = e })
		}

	case *ast.TypeSpec:
		c.convertAST(n.Type, nil)

	case *ast.GenDecl:
		for _, s := range n.Specs {
			switch s := s.(type) {
			case *ast.ImportSpec:
				c.convertAST(s, nil)
			case *ast.ValueSpec:
				c.convertAST(s, func(e ast.Expr) { s.Type = e })
			case *ast.TypeSpec:
				c.convertAST(s, nil)
			}
		}

	case *ast.FuncDecl:
		// https://github.com/tcard/sgo/issues/13
		// if n.Recv != nil {
		// 	c.convertAST(n.Recv, nil)
		// }
		c.convertAST(n.Type, nil)

	case *ast.File:
		for _, d := range n.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				c.convertAST(d, nil)
			case *ast.FuncDecl:
				c.convertAST(d, func(e ast.Expr) {
					if e, ok := e.(*ast.FuncType); ok {
						d.Type = e
					}
				})
			}
		}
	}
}

func (c *astConverter) maybeReplace(node ast.Node, replace func(e ast.Expr)) bool {
	if replace == nil {
		return false
	}

	n := reflect.ValueOf(node)

	if n.Elem().Type().Kind() != reflect.Struct {
		return false
	}

	doc := n.Elem().FieldByName("Doc")
	if !doc.IsValid() {
		return false
	}

	cg := doc.Interface().(*ast.CommentGroup)
	if cg == nil {
		return false
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
			return false
		}

		replace(e)
		return true
	}

	return false
}
