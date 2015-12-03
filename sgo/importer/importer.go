// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package importer provides access to export data importers.
package importer

import (
	"errors"
	"fmt"
	goast "go/ast"
	goconstant "go/constant"
	goimporter "go/importer"
	goparser "go/parser"
	gotoken "go/token"
	gotypes "go/types"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tcard/sgo/sgo/constant"
	"github.com/tcard/sgo/sgo/token"
	"github.com/tcard/sgo/sgo/types"
)

// A Lookup function returns a reader to access package data for
// a given import path, or an error if no matching package is found.
type Lookup func(path string) (io.ReadCloser, error)

// For returns an Importer for the given compiler and lookup interface,
// or nil. Supported compilers are "gc", and "gccgo". If lookup is nil,
// the default package lookup mechanism for the given compiler is used.
func For(compiler string, lookup Lookup) types.Importer {
	wrapped := goimporter.For(compiler, goimporter.Lookup(lookup))
	if wrapped == nil {
		return nil
	}
	return &importer{wrapped}
}

// Default returns an Importer for the compiler that built the running binary.
func Default() types.Importer {
	return For(runtime.Compiler, nil)
}

type importer struct {
	imp gotypes.Importer
}

func (imp *importer) Import(path string) (*types.Package, error) {
	gopath := append([]string{os.Getenv("GOROOT")}, strings.Split(os.Getenv("GOPATH"), ":")...)
	gopkg, anns, err := imp.importFromSrc(path, gopath)
	if err != nil {
		gopkg, err = imp.imp.Import(path)
	}
	if err != nil {
		return nil, err
	}
	if anns == nil {
		anns = map[string]annotation{}
	}
	conv := &converser{gopkg: gopkg, anns: anns}
	return conv.convert(), nil

}

func (imp *importer) importFromSrc(path string, gopath []string) (*gotypes.Package, map[string]annotation, error) {
	for _, p := range gopath {
		fullPath := filepath.Join(p, "src", path)
		fset := gotoken.NewFileSet()
		pkgs, err := goparser.ParseDir(fset, fullPath, nil, goparser.ParseComments)
		if err != nil {
			continue
		}
		astPkg, ok := pkgs[path[strings.LastIndex(path, "/")+1:]]
		if !ok {
			continue
		}
		cfg := &gotypes.Config{
			Importer: imp.imp,
		}
		var files []*goast.File
		for _, file := range astPkg.Files {
			files = append(files, file)
		}
		pkg, err := cfg.Check(path, fset, files, &gotypes.Info{})
		if err != nil {
			continue
		}
		annotations := stdAnnotations(pkg, path)
		if annotations == nil {
			annotations = annotationsFromAST(pkg, files)
		}
		return pkg, annotations, nil
	}
	return nil, nil, errors.New("package " + path + " not found")
}

type converser struct {
	gopkg     *gotypes.Package
	ret       *types.Package
	anns      map[string]annotation
	converted map[interface{}]interface{}
	ifaces    []*types.Interface
}

func (c *converser) convert() *types.Package {
	c.converted = map[interface{}]interface{}{}
	return c.convertPackage(c.gopkg)
}

func (c *converser) convertPackage(v *gotypes.Package) *types.Package {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Package)
	}

	ret := types.NewPackage(v.Path(), v.Name())
	if c.ret == nil {
		c.ret = ret
	}
	c.converted[v] = ret

	var imports []*types.Package
	for _, imported := range v.Imports() {
		imports = append(imports, c.convertPackage(imported))
	}
	ret.SetImports(imports)

	c.convertScope(ret.Scope(), v.Scope())

	for _, iface := range c.ifaces {
		iface.Complete()
	}

	return ret
}

func (c *converser) convertScope(dst *types.Scope, src *gotypes.Scope) {
	for _, name := range src.Names() {
		dst.Insert(c.convertObject(src.Lookup(name)))
	}
	for i := 0; i < src.NumChildren(); i++ {
		child := src.Child(i)
		newScope := types.NewScope(dst, token.Pos(child.Pos()), token.Pos(child.End()), "")
		c.convertScope(newScope, child)
	}
}

func (c *converser) convertObject(v gotypes.Object) types.Object {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(types.Object)
	}
	var ret types.Object
	switch v := v.(type) {
	case *gotypes.Func:
		ret = c.convertFunc(v)
	case *gotypes.TypeName:
		ret = c.convertTypeName(v)
	case *gotypes.Var:
		ret = c.convertVar(v)
	case *gotypes.Const:
		ret = c.convertConst(v)
	case *gotypes.PkgName:
		ret = c.convertPkgName(v)
	default:
		panic(fmt.Sprintf("unhandled Object %T", v))
	}
	c.converted[v] = ret
	return ret
}

func (c *converser) convertFunc(v *gotypes.Func) *types.Func {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Func)
	}
	ret := types.NewFunc(
		token.Pos(v.Pos()),
		c.ret,
		v.Name(),
		c.convertSignature(v.Type().(*gotypes.Signature)),
	)
	c.converted[v] = ret
	return ret
}

func (c *converser) convertSignature(v *gotypes.Signature) *types.Signature {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Signature)
	}
	ret := types.NewSignature(
		c.convertParamVar(v.Recv()),
		c.convertTuple(v.Params(), c.convertParamVar),
		c.convertTuple(v.Results(), c.convertParamVar),
		v.Variadic(),
	)
	c.converted[v] = ret
	return ret
}

func (c *converser) convertParamVar(v *gotypes.Var) *types.Var {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Var)
	}
	ret := types.NewParam(
		token.Pos(v.Pos()),
		c.ret,
		v.Name(),
		c.convertType(v.Type()),
	)
	c.converted[v] = ret
	return ret
}

func (c *converser) convertVar(v *gotypes.Var) *types.Var {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Var)
	}
	ret := types.NewVar(
		token.Pos(v.Pos()),
		c.ret,
		v.Name(),
		c.convertType(v.Type()),
	)
	c.converted[v] = ret
	return ret
}

func (c *converser) convertConst(v *gotypes.Const) *types.Const {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Const)
	}
	ret := types.NewConst(
		token.Pos(v.Pos()),
		c.ret,
		v.Name(),
		c.convertType(v.Type()),
		c.convertConstantValue(v.Val()),
	)
	c.converted[v] = ret
	return ret
}

func (c *converser) convertPkgName(v *gotypes.PkgName) *types.PkgName {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.PkgName)
	}
	ret := types.NewPkgName(
		token.Pos(v.Pos()),
		c.ret,
		v.Name(),
		c.convertPackage(v.Imported()),
	)
	c.converted[v] = ret
	return ret
}

func (c *converser) convertTuple(v *gotypes.Tuple, conv func(*gotypes.Var) *types.Var) *types.Tuple {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Tuple)
	}
	vars := make([]*types.Var, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		vars = append(vars, conv(v.At(i)))
	}
	ret := types.NewTuple(vars...)
	c.converted[v] = ret
	return ret
}

func (c *converser) convertTypeName(v *gotypes.TypeName) *types.TypeName {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.TypeName)
	}

	// This part is a bit tricky. If NewTypeName's typ argument is nil, the
	// function makes it recursive with the newly created *TypeName. So if
	// we get a *TypeName whose Type() is a *Named whose Obj() is the same
	// *TypeName, we know it was constructed this way, so do the same.
	// Otherwise we get in a infinite recursion converting the *TypeName's
	// type.
	var typ types.Type
	if named, ok := v.Type().(*gotypes.Named); !ok || named.Obj() != v {
		typ = c.convertType(v.Type())
	}

	ret := types.NewTypeName(
		token.Pos(v.Pos()),
		c.convertPackage(v.Pkg()),
		v.Name(),
		typ,
	)
	c.converted[v] = ret
	return ret
}

func (c *converser) convertType(v gotypes.Type) types.Type {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(types.Type)
	}
	var ret types.Type
	switch v := v.(type) {
	case *gotypes.Named:
		ret = c.convertNamed(v)
	case *gotypes.Pointer:
		ret = c.convertPointer(v)
	case *gotypes.Basic:
		ret = c.convertBasic(v)
	case *gotypes.Struct:
		ret = c.convertStruct(v)
	case *gotypes.Interface:
		ret = c.convertInterface(v)
	case *gotypes.Slice:
		ret = c.convertSlice(v)
	case *gotypes.Array:
		ret = c.convertArray(v)
	case *gotypes.Signature:
		ret = c.convertSignature(v)
	case *gotypes.Chan:
		ret = c.convertChan(v)
	case *gotypes.Map:
		ret = c.convertMap(v)
	default:
		panic(fmt.Sprintf("unhandled Type %T", v))
	}
	c.converted[v] = ret
	return ret
}

func (c *converser) convertNamed(v *gotypes.Named) *types.Named {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Named)
	}
	if gotypes.Universe.Lookup("error").(*gotypes.TypeName).Type().(*gotypes.Named) == v {
		return types.Universe.Lookup("error").(*types.TypeName).Type().(*types.Named)
	}
	ret := types.NewNamed(
		c.convertTypeName(v.Obj()),
		nil,
		nil,
	)
	c.converted[v] = ret
	for i := 0; i < v.NumMethods(); i++ {
		ret.AddMethod(c.convertFunc(v.Method(i)))
	}
	ret.SetUnderlying(c.convertType(v.Underlying()))
	return ret
}

func (c *converser) convertPointer(v *gotypes.Pointer) *types.Pointer {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Pointer)
	}
	ret := types.NewPointer(c.convertType(v.Elem()))
	c.converted[v] = ret
	return ret
}

func (c *converser) convertBasic(v *gotypes.Basic) *types.Basic {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Basic)
	}
	var ret *types.Basic
	for i, b := range gotypes.Typ {
		if v == b {
			ret = types.Typ[i]
			break
		}
	}
	switch v.Kind() {
	case gotypes.Byte:
		ret = types.ByteType
	case gotypes.Rune:
		ret = types.RuneType
	}
	if ret == nil {
		panic(fmt.Sprintf("unknown basic type %v", v))
	}
	c.converted[v] = ret
	return ret
}

func (c *converser) convertStruct(v *gotypes.Struct) *types.Struct {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Struct)
	}
	fields := make([]*types.Var, 0, v.NumFields())
	tags := make([]string, 0, v.NumFields())
	for i := 0; i < v.NumFields(); i++ {
		fields = append(fields, c.convertVar(v.Field(i)))
		tags = append(tags, v.Tag(i))
	}
	ret := types.NewStruct(fields, tags)
	c.converted[v] = ret
	return ret
}

func (c *converser) convertInterface(v *gotypes.Interface) *types.Interface {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Interface)
	}
	ret := types.NewInterface(nil, nil)
	c.converted[v] = ret
	for i := 0; i < v.NumExplicitMethods(); i++ {
		ret.AddMethod(c.convertFunc(v.ExplicitMethod(i)))
	}
	for i := 0; i < v.NumEmbeddeds(); i++ {
		ret.AddEmbedded(c.convertNamed(v.Embedded(i)))
	}
	c.ifaces = append(c.ifaces, ret)
	return ret
}

func (c *converser) convertSlice(v *gotypes.Slice) *types.Slice {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Slice)
	}
	ret := types.NewSlice(c.convertType(v.Elem()))
	c.converted[v] = ret
	return ret
}

func (c *converser) convertArray(v *gotypes.Array) *types.Array {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Array)
	}
	ret := types.NewArray(c.convertType(v.Elem()), v.Len())
	c.converted[v] = ret
	return ret
}

func (c *converser) convertChan(v *gotypes.Chan) *types.Chan {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Chan)
	}
	ret := types.NewChan(types.ChanDir(v.Dir()), c.convertType(v.Elem()))
	c.converted[v] = ret
	return ret
}

func (c *converser) convertMap(v *gotypes.Map) *types.Map {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.Map)
	}
	ret := types.NewMap(c.convertType(v.Key()), c.convertType(v.Elem()))
	c.converted[v] = ret
	return ret
}

func (c *converser) convertConstantValue(v goconstant.Value) constant.Value {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(constant.Value)
	}
	var ret constant.Value
	switch v.Kind() {
	case goconstant.Bool:
		ret = constant.MakeBool(goconstant.BoolVal(v))
	case goconstant.String:
		ret = constant.MakeString(goconstant.StringVal(v))
	case goconstant.Int:
		ret = constant.MakeFromLiteral(v.String(), token.INT, 0)
	case goconstant.Float:
		ret = constant.MakeFromLiteral(v.String(), token.FLOAT, 0)
	case goconstant.Complex:
		ret = constant.MakeFromLiteral(v.String(), token.IMAG, 0)
	}
	c.converted[v] = ret
	return ret
}
