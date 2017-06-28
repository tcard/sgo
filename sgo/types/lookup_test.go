package types

import (
	"reflect"
	"testing"

	"github.com/tcard/sgo/sgo/ast"
	"github.com/tcard/sgo/sgo/parser"
	"github.com/tcard/sgo/sgo/token"
)

func TestFindOptionables(t *testing.T) {
	for _, c := range []struct {
		testName            string
		typ                 string
		expectedCheckable   []string
		expectedUncheckable []string
	}{
		{
			testName:            "simple pointer",
			typ:                 "*int",
			expectedCheckable:   []string{""},
			expectedUncheckable: nil,
		},
		{
			testName:            "pointer to pointer",
			typ:                 "**int",
			expectedCheckable:   []string{"", "pointee"},
			expectedUncheckable: nil,
		},
		{
			testName:            "pointer to optional pointer",
			typ:                 "*?*int",
			expectedCheckable:   []string{""},
			expectedUncheckable: nil,
		},
		{
			testName:            "optional pointer",
			typ:                 "?*int",
			expectedCheckable:   nil,
			expectedUncheckable: nil,
		},
		{
			testName:            "map",
			typ:                 "map[int]string",
			expectedCheckable:   []string{""},
			expectedUncheckable: nil,
		},
		{
			testName:            "map from pointer",
			typ:                 "map[*int]string",
			expectedCheckable:   []string{""},
			expectedUncheckable: []string{"key"},
		},
		{
			testName:            "map from optional pointer",
			typ:                 "map[?*int]string",
			expectedCheckable:   []string{""},
			expectedUncheckable: nil,
		},
		{
			testName:            "pointer to complex struct",
			typ:                 "*struct{x int; y chan int; z chan *int}",
			expectedCheckable:   []string{"", "pointee's field y", "pointee's field z"},
			expectedUncheckable: []string{"pointee's field z's element"},
		},
		{
			testName:            "func",
			typ:                 "func(x int, y *int) (int, func(*int))",
			expectedCheckable:   []string{""},
			expectedUncheckable: []string{"#2 argument", "#2 return type", "#2 return type's #1 argument"},
		},
		{
			testName:            "func everything wrapped",
			typ:                 "func(x int, y ?*int) (int, ?func(?*int))",
			expectedCheckable:   []string{""},
			expectedUncheckable: nil,
		},
		{
			testName:            "interface",
			typ:                 "interface { M(x int, y *int) (int, func()) }",
			expectedCheckable:   []string{""},
			expectedUncheckable: []string{"method M's #2 argument", "method M's #2 return type"},
		},
		{
			testName:            "named",
			typ:                 "struct{x int; y error; z ?error; ch chan error}",
			expectedCheckable:   []string{"field y", "field ch"},
			expectedUncheckable: []string{"field ch's element"},
		},
	} {
		t.Run("testName="+c.testName, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "", `package main; var X `+c.typ, 0)
			if err != nil {
				t.Fatal(err)
			}
			var conf Config
			defs := make(map[*ast.Ident]Object)
			_, err = conf.Check(f.Name.Name, fset, []*ast.File{f}, &Info{Defs: defs})
			if err != nil {
				t.Fatal(err)
			}

			var typ Type
			for id, o := range defs {
				if id.Name == "X" {
					typ = o.(*Var).Type()
				}
			}

			checkable, uncheckable := FindOptionables(typ)
			if expected, got := asStrings(checkable), c.expectedCheckable; !reflect.DeepEqual(expected, got) {
				t.Errorf("checkable: expected %#v, got %#v", expected, got)
			}
			if expected, got := asStrings(uncheckable), c.expectedUncheckable; !reflect.DeepEqual(expected, got) {
				t.Errorf("uncheckable: expected %#v, got %#v", expected, got)
			}
		})
	}
}

func asStrings(ps []OptionablePath) []string {
	if ps == nil {
		return nil
	}
	ss := make([]string, 0, len(ps))
	for _, p := range ps {
		ss = append(ss, p.String())
	}
	return ss
}
