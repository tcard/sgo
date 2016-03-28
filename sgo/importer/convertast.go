package importer

import (
	"reflect"
	"strings"

	"github.com/tcard/sgo/sgo/annotations"
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
func ConvertAST(a *ast.File, info *types.Info, ann *annotations.Annotation) {
	c := astConverter{info: info}
	c.convertAST(a, ann, nil)
}

type astConverter struct {
	info      *types.Info
	converted map[interface{}]struct{}
}

func (c *astConverter) convertAST(node ast.Node, ann *annotations.Annotation, replace func(e ast.Expr)) {
	if replaced := c.maybeReplace(node, ann, replace); replaced {
		return
	}

	switch n := node.(type) {
	case *ast.Field:
		if len(n.Names) == 1 {
			ann = ann.Lookup(n.Names[0].Name)
		} else {
			ann = nil
		}
		c.convertAST(n.Type, ann, func(e ast.Expr) { n.Type = e })

	case *ast.FieldList:
		for _, f := range n.List {
			c.convertAST(f, ann, nil)
		}

	case *ast.StarExpr:
		if replace != nil {
			replace(&ast.OptionalType{Elt: n})
		}
		c.convertAST(n.X, ann, func(e ast.Expr) { n.X = e })

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
		c.convertAST(n.Elt, ann, func(e ast.Expr) { n.Elt = e })

	case *ast.StructType:
		c.convertAST(n.Fields, ann, nil)

	case *ast.FuncType:
		if replace != nil {
			replace(&ast.OptionalType{Elt: n})
		}
		if n.Params != nil {
			c.convertAST(n.Params, ann, nil)
		}
		if n.Results != nil {
			c.convertAST(n.Results, ann, nil)
		}

	case *ast.InterfaceType:
		if replace != nil {
			replace(&ast.OptionalType{Elt: n})
		}
		for _, f := range n.Methods.List {
			name := ""
			if len(f.Names) == 0 {
				var id *ast.Ident

				switch t := f.Type.(type) {
				case *ast.Ident:
					id = t
				case *ast.StarExpr:
					if t, ok := t.X.(*ast.Ident); ok {
						id = t
					}
				}

				if id != nil {
					name = id.Name
				}
			} else {
				name = f.Names[0].Name
			}
			c.convertAST(f.Type, ann.Lookup(name), nil)
		}

	case *ast.MapType:
		if replace != nil {
			replace(&ast.OptionalType{Elt: n})
		}
		c.convertAST(n.Key, ann, func(e ast.Expr) { n.Key = e })
		c.convertAST(n.Value, ann, func(e ast.Expr) { n.Value = e })

	case *ast.ChanType:
		if replace != nil {
			replace(&ast.OptionalType{Elt: n})
		}
		c.convertAST(n.Value, ann, func(e ast.Expr) { n.Value = e })

	// Declarations
	case *ast.ValueSpec:
		if n.Type != nil {
			c.convertAST(n.Type, nil, func(e ast.Expr) { n.Type = e })
		}

	case *ast.TypeSpec:
		if n.Type != nil {
			// c.convertAST(n.Type, ann, func(e ast.Expr) { n.Type = e })
			c.convertAST(n.Type, ann, nil)
		}

	case *ast.GenDecl:
		for _, s := range n.Specs {
			switch s := s.(type) {
			case *ast.ImportSpec:
				c.convertAST(s, nil, nil)
			case *ast.ValueSpec:
				if replace == nil { // First time.
					c.convertAST(n, ann.Lookup(s.Names[0].Name), func(e ast.Expr) { s.Type = e })
				} else { // Second time.
					c.convertAST(s, ann, replace)
				}
			case *ast.TypeSpec:
				if replace == nil { // First time.
					c.convertAST(n, ann.Lookup(s.Name.Name), func(e ast.Expr) { s.Type = e })
				} else { // Second time.
					c.convertAST(s, ann, replace)
				}
			}
		}

	case *ast.FuncDecl:
		// https://github.com/tcard/sgo/issues/13
		// if n.Recv != nil {
		// 	c.convertAST(n.Recv, nil)
		// }
		c.convertAST(n.Type, nil, nil)

	case *ast.File:
		for _, d := range n.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				c.convertAST(d, ann, nil)
			case *ast.FuncDecl:
				name := d.Name.Name
				if d.Recv != nil && len(d.Recv.List) > 0 {
					switch t := d.Recv.List[0].Type.(type) {
					case *ast.StarExpr:
						if id, ok := t.X.(*ast.Ident); ok {
							name = "(*" + id.Name + ")." + name
						}
					case *ast.Ident:
						name = t.Name + "." + name
					}
				}
				c.convertAST(d, ann.Lookup(name), func(e ast.Expr) {
					if e, ok := e.(*ast.FuncType); ok {
						d.Type = e
					}
				})
			}
		}
	}
}

func (c *astConverter) maybeReplace(node ast.Node, ann *annotations.Annotation, replace func(e ast.Expr)) bool {
	if replace == nil {
		return false
	}

	if typ, ok := ann.Type(); ok {
		e, err := parser.ParseExpr(typ)
		if err == nil {
			replace(e)
			return true
		}
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
