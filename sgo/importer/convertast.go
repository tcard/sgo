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
		if replaced := c.maybeReplace(n, ann, func(e ast.Expr) { n.Type = e }); replaced {
			return
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
		if replace != nil && types.IsOptionable(tn.Type()) {
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
			// Call maybeReplace here because, if it won't replace anything, we don't
			// want to pass a replace function to convertAST as it would think that
			// we're converting a function literal and make it optional by default.
			if replaced := c.maybeReplace(f.Type, ann.Lookup(name), func(e ast.Expr) { f.Type = e }); replaced {
				return
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
				if len(n.Specs) == 1 {
					if replace == nil { // First time.
						c.convertAST(n, ann.Lookup(s.Names.List[0].Name), func(e ast.Expr) { s.Type = e })
					} else { // Second time.
						c.convertAST(s, ann, replace)
					}
				} else {
					c.convertAST(s, ann.Lookup(s.Names.List[0].Name), func(e ast.Expr) { s.Type = e })
				}
			case *ast.TypeSpec:
				if len(n.Specs) == 1 {
					if replace == nil && len(n.Specs) == 1 { // First time.
						c.convertAST(n, ann.Lookup(s.Name.Name), func(e ast.Expr) { s.Type = e })
					} else { // Second time.
						c.convertAST(s, ann, replace)
					}
				} else {
					c.convertAST(s, ann.Lookup(s.Name.Name), func(e ast.Expr) { s.Type = e })
				}
			}
		}

	case *ast.FuncDecl:
		if n.Recv != nil {
			if replaced := c.maybeReplaceFuncDecl(n, ann, func(fun *ast.FuncType, recv ast.Expr) {
				n.Type = fun
				n.Recv.List[0].Type = recv
			}); replaced {
				return
			}

			recv := n.Recv.List[0]
			switch typ := recv.Type.(type) {
			case *ast.StarExpr:
				recv.Type = &ast.OptionalType{Elt: typ}
			case *ast.Ident:
				c.convertAST(recv.Type, nil, func(e ast.Expr) { recv.Type = e })
			}
		}

		// Call maybeReplace here because, if it won't replace anything, we don't
		// want to pass a replace function to convertAST as it would think that
		// we're converting a function literal and make it optional by default.
		if replaced := c.maybeReplace(n.Type, ann, func(e ast.Expr) { n.Type = e.(*ast.FuncType) }); replaced {
			return
		}
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

	s, ok := annFromDoc(node)
	if !ok {
		return false
	}

	e, err := parser.ParseExpr(s)
	if err != nil {
		return false
	}

	replace(e)
	return true
}

func (c *astConverter) maybeReplaceFuncDecl(node *ast.FuncDecl, ann *annotations.Annotation, replace func(fun *ast.FuncType, recv ast.Expr)) bool {
	if replace == nil {
		return false
	}

	if typ, ok := ann.Type(); ok {
		fun, recv, err := parser.ParseMethodExprs(typ)
		if err == nil {
			replace(fun, recv)
			return true
		}
	}

	s, ok := annFromDoc(node)
	if !ok {
		return false
	}

	fun, recv, err := parser.ParseMethodExprs(s)
	if err != nil {
		return false
	}

	replace(fun, recv)
	return true
}

func annFromDoc(node ast.Node) (string, bool) {
	n := reflect.ValueOf(node)

	if n.Elem().Type().Kind() != reflect.Struct {
		return "", false
	}

	doc := n.Elem().FieldByName("Doc")
	if !doc.IsValid() {
		return "", false
	}

	cg := doc.Interface().(*ast.CommentGroup)
	if cg == nil {
		return "", false
	}

	for _, l := range cg.List {
		s := l.Text
		s = strings.TrimPrefix(s, "//")
		s = strings.TrimPrefix(s, "/*")
		s = strings.TrimSpace(s)

		if !strings.HasPrefix(s, "For SGo: ") {
			continue
		}

		return s[len("For SGo: "):], true
	}

	return "", false
}
