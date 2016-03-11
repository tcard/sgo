// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package importer provides access to export data importers.
package importer

import (
	"fmt"
	"go/build"
	goconstant "go/constant"
	goimporter "go/importer"
	gotypes "go/types"
	"os"
	"path/filepath"
	"strings"

	"github.com/tcard/sgo/sgo/ast"
	"github.com/tcard/sgo/sgo/constant"
	"github.com/tcard/sgo/sgo/parser"
	"github.com/tcard/sgo/sgo/token"
	"github.com/tcard/sgo/sgo/types"
)

// Default returns a types.Importer that imports from Go source code and
// transforms to SGo.
//
// For packages imported from any of the passed files, conversion is performed
// by passing the AST through ConvertAST. The packages that imported packages
// import themselves are imported by the default go/importer, without
// transformation to SGo at all, unless they're also imported by those files.
func Default(files []*ast.File) types.Importer {
	visiblePaths := map[string]struct{}{}
	for _, file := range files {
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			if genDecl.Tok != token.IMPORT {
				continue
			}
			for _, spec := range genDecl.Specs {
				path := strings.Trim(spec.(*ast.ImportSpec).Path.Value, "\"`")
				visiblePaths[path] = struct{}{}
			}
		}
	}

	return &importer{
		imported:     map[string]*types.Package{},
		visiblePaths: visiblePaths,
	}
}

type importer struct {
	imported     map[string]*types.Package
	visiblePaths map[string]struct{}
}

func (imp *importer) fromPkg() types.Importer {
	return fromPkg{fromSrc: imp, imp: goimporter.Default()}
}

func (imp *importer) Import(path string) (*types.Package, error) {
	if imported, ok := imp.imported[path]; ok {
		return imported, nil
	}

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

	// 1. Typecheck without fromPkg anything; ConvertAST needs to know
	//    which idents are types to perform the default conversions.

	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	cfg := &types.Config{
		IgnoreFuncBodies:        true,
		IgnoreTopLevelVarValues: true,
		Importer:                imp.fromPkg(),
		AllowUninitializedExprs: true,
	}
	pkg, err := cfg.Check(path, fset, files, info)
	if err != nil {
		return nil, err
	}

	// 2. Convert AST, now using the doc comment annotations and fromPkg
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

	imp.imported[path] = pkg
	return pkg, nil
}

type fromPkg struct {
	fromSrc *importer
	imp     gotypes.Importer
}

func (c fromPkg) Import(path string) (*types.Package, error) {
	if imported, ok := c.fromSrc.imported[path]; ok {
		return imported, nil
	}
	if _, ok := c.fromSrc.visiblePaths[path]; ok {
		return c.fromSrc.Import(path)
	}
	gopkg, err := c.imp.Import(path)
	if err != nil {
		return nil, err
	}
	conv := &converter{gopkg: gopkg}
	conv.convert()
	return conv.ret, nil
}

type converter struct {
	gopkg     *gotypes.Package
	ret       *types.Package
	converted map[interface{}]interface{}
	ifaces    []*types.Interface
}

func (c *converter) convert() *types.Package {
	c.converted = map[interface{}]interface{}{}
	return c.convertPackage(c.gopkg)
}

func (c *converter) convertPackage(v *gotypes.Package) *types.Package {
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

func (c *converter) convertScope(dst *types.Scope, src *gotypes.Scope) {
	for _, name := range src.Names() {
		obj := src.Lookup(name)
		dst.Insert(c.convertObject(obj))
	}
	for i := 0; i < src.NumChildren(); i++ {
		child := src.Child(i)
		newScope := types.NewScope(dst, token.Pos(child.Pos()), token.Pos(child.End()), "")
		c.convertScope(newScope, child)
	}
}

func (c *converter) convertObject(v gotypes.Object) types.Object {
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
	case *gotypes.Builtin:
		ret = c.convertBuiltin(v)
	default:
		panic(fmt.Sprintf("unhandled Object %T", v))
	}
	c.converted[v] = ret
	return ret
}

func (c *converter) convertFunc(v *gotypes.Func) *types.Func {
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

func (c *converter) convertSignature(v *gotypes.Signature) *types.Signature {
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

func (c *converter) convertParamVar(v *gotypes.Var) *types.Var {
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

func (c *converter) convertVar(v *gotypes.Var) *types.Var {
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

func (c *converter) convertConst(v *gotypes.Const) *types.Const {
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

func (c *converter) convertPkgName(v *gotypes.PkgName) *types.PkgName {
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

func (c *converter) convertBuiltin(v *gotypes.Builtin) *types.Builtin {
	switch v.Name() {
	case "Alignof", "Offsetof", "Sizeof":
		return types.Unsafe.Scope().Lookup(v.Name()).(*types.Builtin)
	default:
		return types.Universe.Lookup(v.Name()).(*types.Builtin)
	}
}

func (c *converter) convertTuple(v *gotypes.Tuple, conv func(*gotypes.Var) *types.Var) *types.Tuple {
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

func (c *converter) convertTypeName(v *gotypes.TypeName) *types.TypeName {
	if v == nil {
		return nil
	}
	if v, ok := c.converted[v]; ok {
		return v.(*types.TypeName)
	}

	// This part is a bit tricky. gcimport calls NewTypeName with a nil typ
	// argument, and then calls NewNamed on the resulting *TypeName, which
	// sets its typ to a *Named referring to itself. So if we get a *TypeName
	// whose Type() is a *Named whose Obj() is the same *TypeName, we know it
	// was constructed this way, so we do the same. Otherwise we get into a
	// infinite recursion fromPkg the *TypeName's type.
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
	types.NewNamed(ret, nil, nil)
	c.converted[v] = ret
	return ret
}

func (c *converter) convertType(v gotypes.Type) types.Type {
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

func (c *converter) convertNamed(v *gotypes.Named) *types.Named {
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

func (c *converter) convertPointer(v *gotypes.Pointer) *types.Pointer {
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

func (c *converter) convertBasic(v *gotypes.Basic) *types.Basic {
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

func (c *converter) convertStruct(v *gotypes.Struct) *types.Struct {
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

func (c *converter) convertInterface(v *gotypes.Interface) *types.Interface {
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

func (c *converter) convertSlice(v *gotypes.Slice) *types.Slice {
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

func (c *converter) convertArray(v *gotypes.Array) *types.Array {
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

func (c *converter) convertChan(v *gotypes.Chan) *types.Chan {
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

func (c *converter) convertMap(v *gotypes.Map) *types.Map {
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

func (c *converter) convertConstantValue(v goconstant.Value) constant.Value {
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
