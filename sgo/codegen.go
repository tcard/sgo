package sgo

import (
	"bytes"
	"fmt"
	"go/build"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/tcard/sgo/sgo/ast"
	"github.com/tcard/sgo/sgo/importer"
	"github.com/tcard/sgo/sgo/importpaths"
	"github.com/tcard/sgo/sgo/parser"
	"github.com/tcard/sgo/sgo/printer"
	"github.com/tcard/sgo/sgo/scanner"
	"github.com/tcard/sgo/sgo/token"
	"github.com/tcard/sgo/sgo/types"
)

// For SGo: func(paths []string) (created []string, warnings []error, errs []error)
func TranslatePaths(paths []string) (created []string, warnings []error, errs []error) {
	cwd, err := os.Getwd()
	if err != nil {
		errs = append(errs, err)
		return
	}

	paths, warnings = importpaths.ImportPaths(paths)
	for _, path := range paths {
		pkg, err := build.Default.Import(path, cwd, build.FindOnly|build.IgnoreVendor)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		transCreated, transErrs := TranslateDir(pkg.Dir)
		created = append(created, transCreated...)
		errs = append(errs, transErrs...)
	}
	return created, warnings, errs
}

// For SGo: func(dirName string) ([]string, []error)
func TranslateDir(dirName string) ([]string, []error) {
	var errs []error
	var paths []string

	dir, err := os.Open(dirName)
	if err != nil {
		return nil, []error{err}
	}
	fileNames, err := dir.Readdirnames(-1)
	dir.Close()
	if err != nil {
		return nil, []error{err}
	}
	for _, fileName := range fileNames {
		ext := filepath.Ext(fileName)
		if ext != ".sgo" {
			continue
		}
		paths = append(paths, filepath.Join(dirName, fileName))
	}
	if err != nil {
		errs = append(errs, err)
		return nil, errs
	}
	return TranslateFilePathsFrom(dirName, paths...)
}

// For SGo: func(paths ...string) ([]string, []error)
func TranslateFilePaths(paths ...string) ([]string, []error) {
	return TranslateFilePathsFrom("", paths...)
}

// For SGo: func(whence string, paths ...string) ([]string, []error)
func TranslateFilePathsFrom(whence string, paths ...string) ([]string, []error) {
	var named []NamedFile

	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, []error{err}
		}
		defer f.Close()
		named = append(named, NamedFile{path, f})
	}

	translated, errs := TranslateFilesFrom(whence, named...)
	if len(errs) > 0 {
		return nil, errs
	}

	var created []string
	for i, t := range translated {
		path := named[i].Path
		ext := filepath.Ext(path)
		createdPath := path[:len(path)-len(ext)] + ".go"
		dst, err := os.Create(createdPath)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		created = append(created, createdPath)
		_, err = dst.Write(t)
		if err != nil {
			errs = append(errs, err)
			continue
		}
	}

	return created, errs
}

type NamedFile struct {
	Path string
	// For SGo: io.Reader
	File io.Reader
}

// For SGo: func(files ...NamedFile) ([][]byte, []error)
func TranslateFiles(files ...NamedFile) ([][]byte, []error) {
	return TranslateFilesFrom("", files...)
}

// For SGo: func(whence string, files ...NamedFile) ([][]byte, []error)
func TranslateFilesFrom(whence string, files ...NamedFile) ([][]byte, []error) {
	var errs []error
	fset := token.NewFileSet()

	cwd, err := os.Getwd()
	if err != nil {
		return nil, []error{err}
	}

	var parsed []*ast.File
	var srcs [][]byte
	for _, named := range files {
		src, err := ioutil.ReadAll(named.File)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		relPath, err := filepath.Rel(cwd, named.Path)
		if err != nil {
			relPath = named.Path
		}
		file, err := parser.ParseFile(fset, relPath, src, parser.ParseComments)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		srcs = append(srcs, src)
		parsed = append(parsed, file)
	}

	// Early typecheck, because fileWithAnnotationComments adds lines and
	// then type errors are reported in the wrong line.
	_, typeErrs := typecheck("translate", fset, whence, parsed...)
	if len(typeErrs) > 0 {
		errs = append(errs, makeErrList(fset, typeErrs))
		return nil, errs
	}

	fset = token.NewFileSet() // fileWithAnnotationComments will reparse.
	for i, p := range parsed {
		var err error
		srcs[i], parsed[i], err = fileWithAnnotationComments(p, fset, srcs[i])
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}

	info, typeErrs := typecheck("translate", fset, whence, parsed...)
	if len(typeErrs) > 0 {
		errs = append(errs, makeErrList(fset, typeErrs))
		return nil, errs
	}

	return translate(info, srcs, parsed), errs
}

// For SGo: func(w func() (io.Writer \ error), r io.Reader, filename string) []error
func TranslateFile(w func() (io.Writer, error), r io.Reader, filename string) []error {
	gen, errs := TranslateFiles(NamedFile{filename, r})
	if len(errs) > 0 {
		return errs
	}

	to, err := w()
	if err != nil {
		return []error{err}
	}

	_, err = to.Write(gen[0])
	if err != nil {
		return []error{err}
	}

	return nil
}

func makeErrList(fset *token.FileSet, errs []error) scanner.ErrorList {
	var errList scanner.ErrorList
	for _, err := range errs {
		if v, ok := err.(*types.Error); ok {
			errList = append(errList, &scanner.Error{
				Pos: fset.Position(v.Pos),
				Msg: v.Msg,
			})
		} else {
			errList = append(errList, &scanner.Error{
				Pos: token.Position{},
				Msg: err.Error(),
			})
		}
	}
	return errList
}

func typecheck(path string, fset *token.FileSet, whence string, sgoFiles ...*ast.File) (*types.Info, []error) {
	var errors []error
	imp, err := importer.Default(sgoFiles, whence)
	if err != nil {
		return nil, []error{err}
	}
	cfg := &types.Config{
		Error: func(err error) {
			errors = append(errors, err)
		},
		Importer: imp,
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Implicits:  map[ast.Node]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Scopes:     map[ast.Node]*types.Scope{},
		InitOrder:  []*types.Initializer{},
	}
	_, err = cfg.Check(path, fset, sgoFiles, info)
	if err != nil {
		return nil, errors
	}
	return info, nil
}

func translate(info *types.Info, srcs [][]byte, sgoFiles []*ast.File) [][]byte {
	dsts := make([][]byte, 0, len(sgoFiles))
	for i, sgoFile := range sgoFiles {
		dsts = append(dsts, convertAST(info, srcs[i], sgoFile))
	}
	return dsts
}

func fileWithAnnotationComments(file *ast.File, fset *token.FileSet, src []byte) ([]byte, *ast.File, error) {
	// TODO: So this is an extremely hacky way of doing this. We're going to
	// add the comments directly to the source comments, as text, and then
	// we're going to re-parse it. This is because I tried manipulating the
	// AST, adding the commments there an shifting the nodes' positions, but
	// doing that right is very very convoluted; you need to be tracking all
	// the time where you are, where you _were_, figure out where's a line
	// break, etc. So, well, this will do for now.
	var err error
	var dstChunks [][]byte
	var lastChunkEnd int
	skipNextSpec := false
	addDoc := func(node ast.Node, name *ast.Ident, typ ast.Expr) {
		if typ == nil {
			return
		}
		if name != nil && len(name.Name) > 0 {
			c := name.Name[0]
			if !(c >= 'A' && c <= 'Z') {
				return
			}
		}
		buf := &bytes.Buffer{}
		err = printer.Fprint(buf, fset, typ)
		if err != nil {
			return
		}
		pos := int(node.Pos() - file.Pos())
		var space []byte
		for i := pos - 1; i >= 0 && (src[i] == ' ' || src[i] == '\t'); i-- {
			space = append([]byte{src[i]}, space...)
		}
		text := append([]byte("// For SGo: "+buf.String()+"\n"), space...)
		dstChunks = append(dstChunks, src[lastChunkEnd:pos], text)
		lastChunkEnd = pos
	}
	var visitor visitorFunc
	visitor = visitorFunc(func(node ast.Node) (w ast.Visitor) {
		var typ ast.Expr
		var name *ast.Ident
		switch node := node.(type) {
		case *ast.FuncDecl:
			typ = node.Type
			name = node.Name
		case *ast.GenDecl:
			if node.Lparen != 0 || node.Tok == token.IMPORT || node.Tok == token.CONST {
				return visitor
			}
			switch spec := node.Specs[0].(type) {
			case *ast.TypeSpec:
				skipNextSpec = true
				typ = spec.Type
				name = spec.Name
			case *ast.ValueSpec:
				skipNextSpec = true
				typ = spec.Type
				if len(spec.Names) > 0 {
					name = spec.Names[0]
				}
			}
			switch typ.(type) {
			case *ast.InterfaceType, *ast.StructType:
				return visitor
			}
		case *ast.InterfaceType:
			for i := 0; i < len(node.Methods.List); i++ {
				item := node.Methods.List[i]
				if len(item.Names) > 0 {
					name = item.Names[0]
				}
				addDoc(item, name, item.Type)
			}
			return visitor
		case *ast.StructType:
			for i := 0; i < len(node.Fields.List); i++ {
				item := node.Fields.List[i]
				if len(item.Names) > 0 {
					name = item.Names[0]
				}
				addDoc(item, name, item.Type)
			}
			return visitor
		case *ast.TypeSpec:
			if skipNextSpec {
				skipNextSpec = false
				return visitor
			}
			typ = node.Type
			name = node.Name
		case *ast.ValueSpec:
			if skipNextSpec {
				skipNextSpec = false
				return visitor
			}
			typ = node.Type
			if len(node.Names) > 0 {
				name = node.Names[0]
			}
		default:
			return visitor
		}

		addDoc(node, name, typ)
		return visitor
	})
	ast.Walk(visitor, file)
	if err != nil {
		return nil, nil, err
	}

	dst := bytes.Join(append(dstChunks, src[lastChunkEnd:]), nil)
	dstFile, err := parser.ParseFile(fset, file.Name.Name, dst, parser.ParseComments)
	return dst, dstFile, err
}

type visitorFunc func(node ast.Node) (w ast.Visitor)

func (v visitorFunc) Visit(node ast.Node) (w ast.Visitor) {
	return v(node)
}

func convertAST(info *types.Info, src []byte, sgoAST *ast.File) []byte {
	c := converter{
		Info: info,
		src:  src,
		base: int(sgoAST.Pos()) - 1,
	}
	c.convertFile(sgoAST)
	return bytes.Join(append(c.dstChunks, src[c.lastChunkEnd:]), nil)
}

type converter struct {
	*types.Info
	lastFunc    *types.Signature
	lastFuncAST *ast.FuncType

	base         int
	src          []byte
	dstChunks    [][]byte
	lastChunkEnd int
}

func (c *converter) convertFile(v *ast.File) {
	if v == nil {
		return
	}
	c.convertIdent(v.Name)
	for _, v := range v.Decls {
		c.convertDecl(v)
	}
}

func (c *converter) convertDecl(v ast.Decl) {
	if v == nil {
		return
	}
	switch v := v.(type) {
	case *ast.GenDecl:
		c.convertGenDecl(v)
		return
	case *ast.FuncDecl:
		c.convertFuncDecl(v)
		return
	case *ast.BadDecl:
		c.convertBadDecl(v)
		return
	default:
		panic(fmt.Sprintf("unhandled Decl %T", v))
	}
}

func (c *converter) convertBadDecl(v *ast.BadDecl) {
	if v == nil {
		return
	}

}

func (c *converter) convertGenDecl(v *ast.GenDecl) {
	if v == nil {
		return
	}
	for _, v := range v.Specs {
		c.convertSpec(v)
	}
}

func (c *converter) convertSpec(v ast.Spec) {
	if v == nil {
		return
	}
	switch v := v.(type) {
	case *ast.TypeSpec:
		c.convertTypeSpec(v)
		return
	case *ast.ImportSpec:
		c.convertImportSpec(v)
		return
	case *ast.ValueSpec:
		c.convertValueSpec(v)
		return
	default:
		panic(fmt.Sprintf("unhandled Spec %T", v))
	}
}

func (c *converter) convertTypeSpec(v *ast.TypeSpec) {
	if v == nil {
		return
	}

	c.convertIdent(v.Name)
	c.convertExpr(v.Type)
}

func (c *converter) convertImportSpec(v *ast.ImportSpec) {
	if v == nil {
		return
	}

	c.convertIdent(v.Name)
	c.convertBasicLit(v.Path)
}

func (c *converter) convertValueSpec(v *ast.ValueSpec) {
	if v == nil {
		return
	}
	for _, name := range v.Names {
		c.convertIdent(name)
	}
	c.convertExpr(v.Type)
	c.convertExprList(v.Values)
}

func (c *converter) convertExprList(v *ast.ExprList) {
	if v == nil {
		return
	}
	for i, expr := range v.List {
		if i == v.EntangledPos-1 {
			c.putChunks(int(expr.Pos())-1, c.src[c.lastChunkEnd:int(v.List[i-1].End())-c.base-1], []byte(", "))
		}
		c.convertExpr(expr)
	}
}

func (c *converter) convertFuncDecl(v *ast.FuncDecl) {
	if v == nil {
		return
	}
	c.convertFieldList(v.Recv)
	c.convertFuncType(v.Type)
	c.convertIdent(v.Name)
	unset := c.setLastFunc(c.Info.ObjectOf(v.Name).Type().(*types.Signature), v.Type)
	defer unset()
	c.convertBlockStmt(v.Body)
}

func (c *converter) setLastFunc(sig *types.Signature, astTyp *ast.FuncType) func() {
	oldLastFunc, oldLastFuncAST := c.lastFunc, c.lastFuncAST
	c.lastFunc = sig
	c.lastFuncAST = astTyp
	return func() {
		c.lastFunc, c.lastFuncAST = oldLastFunc, oldLastFuncAST
	}
}

func (c *converter) convertFuncType(v *ast.FuncType) {
	if v == nil {
		return
	}

	c.convertFieldList(v.Params)
	c.convertFieldList(v.Results)
}

func (c *converter) convertStmt(v ast.Stmt) {
	if v == nil {
		return
	}
	switch v := v.(type) {
	case *ast.ReturnStmt:
		c.convertReturnStmt(v)
		return
	case *ast.AssignStmt:
		c.convertAssignStmt(v)
		return
	case *ast.IfStmt:
		c.convertIfStmt(v)
		return
	case *ast.ExprStmt:
		c.convertExprStmt(v)
		return
	case *ast.BlockStmt:
		c.convertBlockStmt(v)
		return
	case *ast.DeclStmt:
		c.convertDeclStmt(v)
		return
	case *ast.TypeSwitchStmt:
		c.convertTypeSwitchStmt(v)
		return
	case *ast.CaseClause:
		c.convertCaseClause(v)
		return
	case *ast.BadStmt:
		c.convertBadStmt(v)
		return
	case *ast.BranchStmt:
		c.convertBranchStmt(v)
		return
	case *ast.CommClause:
		c.convertCommClause(v)
		return
	case *ast.DeferStmt:
		c.convertDeferStmt(v)
		return
	case *ast.EmptyStmt:
		c.convertEmptyStmt(v)
		return
	case *ast.ForStmt:
		c.convertForStmt(v)
		return
	case *ast.GoStmt:
		c.convertGoStmt(v)
		return
	case *ast.IncDecStmt:
		c.convertIncDecStmt(v)
		return
	case *ast.LabeledStmt:
		c.convertLabeledStmt(v)
		return
	case *ast.RangeStmt:
		c.convertRangeStmt(v)
		return
	case *ast.SelectStmt:
		c.convertSelectStmt(v)
		return
	case *ast.SendStmt:
		c.convertSendStmt(v)
		return
	case *ast.SwitchStmt:
		c.convertSwitchStmt(v)
		return
	default:
		panic(fmt.Sprintf("unhandled Stmt %T", v))
	}
}

func (c *converter) convertSwitchStmt(v *ast.SwitchStmt) {
	if v == nil {
		return
	}

	c.convertStmt(v.Init)
	c.convertExpr(v.Tag)
	c.convertBlockStmt(v.Body)
}

func (c *converter) convertSendStmt(v *ast.SendStmt) {
	if v == nil {
		return
	}

	c.convertExpr(v.Chan)
	c.convertExpr(v.Value)
}

func (c *converter) convertSelectStmt(v *ast.SelectStmt) {
	if v == nil {
		return
	}

	c.convertBlockStmt(v.Body)
}

func (c *converter) convertRangeStmt(v *ast.RangeStmt) {
	if v == nil {
		return
	}

	c.convertExpr(v.Key)
	c.convertExpr(v.Value)
	c.convertExpr(v.X)
	c.convertBlockStmt(v.Body)
}

func (c *converter) convertLabeledStmt(v *ast.LabeledStmt) {
	if v == nil {
		return
	}

	c.convertIdent(v.Label)
	c.convertStmt(v.Stmt)
}

func (c *converter) convertIncDecStmt(v *ast.IncDecStmt) {
	if v == nil {
		return
	}

	c.convertExpr(v.X)
}

func (c *converter) convertForStmt(v *ast.ForStmt) {
	if v == nil {
		return
	}

	c.convertStmt(v.Init)
	c.convertExpr(v.Cond)
	c.convertStmt(v.Post)
	c.convertBlockStmt(v.Body)
}

func (c *converter) convertEmptyStmt(v *ast.EmptyStmt) {
	if v == nil {
		return
	}
}

func (c *converter) convertDeferStmt(v *ast.DeferStmt) {
	if v == nil {
		return
	}

	c.convertCallExpr(v.Call)
}

func (c *converter) convertGoStmt(v *ast.GoStmt) {
	if v == nil {
		return
	}

	c.convertCallExpr(v.Call)
}

func (c *converter) convertBranchStmt(v *ast.BranchStmt) {
	if v == nil {
		return
	}

	c.convertIdent(v.Label)
}

func (c *converter) convertCommClause(v *ast.CommClause) {
	if v == nil {
		return
	}
	c.convertStmt(v.Comm)
	for _, v := range v.Body {
		c.convertStmt(v)
	}
}

func (c *converter) convertReturnStmt(v *ast.ReturnStmt) {
	if v == nil {
		return
	}
	if v.Results.EntangledPos == 1 {
		// return \ err
		resultsLen := c.lastFunc.Results().Len()
		results := make([][]byte, 0, resultsLen)
		for i := 0; i < resultsLen; i++ {
			typ := c.lastFunc.Results().At(i).Type()
			switch underlying := typ.Underlying().(type) {
			case *types.Pointer, *types.Map, *types.Slice, *types.Signature, *types.Interface, *types.Optional:
				results = append(results, []byte("nil"))
			case *types.Struct:
				typ := c.lastFuncAST.Results.List[i].Type
				results = append(results, append(c.src[typ.Pos():typ.End()], '{', '}'))
			case *types.Basic:
				info := underlying.Info()
				switch {
				case info&types.IsBoolean != 0:
					results = append(results, []byte("false"))
				case info&types.IsInteger != 0:
					results = append(results, []byte("0"))
				case info&types.IsFloat != 0, info&types.IsComplex != 0:
					results = append(results, []byte("0.0"))
				case info&types.IsString != 0:
					results = append(results, []byte(`""`))
				default:
					results = append(results, []byte("nil"))
				}
			default:
				panic(fmt.Sprintf("unhandled Type %v", typ))
			}
		}
		text := append(bytes.Join(results, []byte(", ")), []byte(", ")...)
		c.putChunks(int(v.Results.Pos())-1, c.src[c.lastChunkEnd:int(v.Pos())-c.base-1+len("return ")], text)
	}
	for _, v := range v.Results.List {
		c.convertExpr(v)
	}
	if v.Results.EntangledPos > 1 {
		// return x, y, z \
		chunk := ", nil"
		if c.lastFunc.Results().Entangled().Type() == types.Typ[types.Bool] {
			chunk = ", true"
		}
		c.putChunks(int(v.Results.End())+1, c.src[c.lastChunkEnd:int(v.Results.End())-c.base-1], []byte(chunk))
	}
}

func (c *converter) convertBadStmt(v *ast.BadStmt) {
	if v == nil {
		return
	}

}

func (c *converter) convertAssignStmt(v *ast.AssignStmt) {
	if v == nil {
		return
	}
	c.convertExprList(v.Lhs)
	c.convertExprList(v.Rhs)
}

func (c *converter) convertDeclStmt(v *ast.DeclStmt) {
	if v == nil {
		return
	}

	c.convertDecl(v.Decl)
}

func (c *converter) convertBlockStmt(v *ast.BlockStmt) {
	if v == nil {
		return
	}
	for _, v := range v.List {
		c.convertStmt(v)
	}
}

func (c *converter) convertTypeSwitchStmt(v *ast.TypeSwitchStmt) {
	if v == nil {
		return
	}

	c.convertStmt(v.Init)
	c.convertStmt(v.Assign)
	c.convertBlockStmt(v.Body)
}

func (c *converter) convertCaseClause(v *ast.CaseClause) {
	if v == nil {
		return
	}
	for _, v := range v.List.List {
		c.convertExpr(v)
	}
	for _, v := range v.Body {
		c.convertStmt(v)
	}
}

func (c *converter) convertIfStmt(v *ast.IfStmt) {
	if v == nil {
		return
	}

	c.convertStmt(v.Init)
	c.convertExpr(v.Cond)
	c.convertBlockStmt(v.Body)
	c.convertStmt(v.Else)
}

func (c *converter) convertExpr(v ast.Expr) {
	if v == nil {
		return
	}
	switch v := v.(type) {
	case *ast.StructType:
		c.convertStructType(v)
		return
	case *ast.Ident:
		c.convertIdent(v)
		return
	case *ast.CallExpr:
		c.convertCallExpr(v)
		return
	case *ast.StarExpr:
		c.convertStarExpr(v)
		return
	case *ast.OptionalType:
		c.convertOptionalType(v)
		return
	case *ast.CompositeLit:
		c.convertCompositeLit(v)
		return
	case *ast.UnaryExpr:
		c.convertUnaryExpr(v)
		return
	case *ast.BasicLit:
		c.convertBasicLit(v)
		return
	case *ast.BinaryExpr:
		c.convertBinaryExpr(v)
		return
	case *ast.SelectorExpr:
		c.convertSelectorExpr(v)
		return
	case *ast.FuncType:
		c.convertFuncType(v)
		return
	case *ast.FuncLit:
		c.convertFuncLit(v)
		return
	case *ast.InterfaceType:
		c.convertInterfaceType(v)
		return
	case *ast.ParenExpr:
		c.convertParenExpr(v)
		return
	case *ast.TypeAssertExpr:
		c.convertTypeAssertExpr(v)
		return
	case *ast.MapType:
		c.convertMapType(v)
		return
	case *ast.IndexExpr:
		c.convertIndexExpr(v)
		return
	case *ast.KeyValueExpr:
		c.convertKeyValueExpr(v)
		return
	case *ast.ArrayType:
		c.convertArrayType(v)
		return
	case *ast.BadExpr:
		c.convertBadExpr(v)
		return
	case *ast.ChanType:
		c.convertChanType(v)
		return
	case *ast.Ellipsis:
		c.convertEllipsis(v)
		return
	case *ast.SliceExpr:
		c.convertSliceExpr(v)
		return
	default:
		panic(fmt.Sprintf("unhandled Expr %T", v))
	}
}

func (c *converter) convertIndexExpr(v *ast.IndexExpr) {
	if v == nil {
		return
	}

	c.convertExpr(v.X)
	c.convertExpr(v.Index)
}

func (c *converter) convertEllipsis(v *ast.Ellipsis) {
	if v == nil {
		return
	}

	c.convertExpr(v.Elt)
}

func (c *converter) convertBadExpr(v *ast.BadExpr) {
	if v == nil {
		return
	}

}

func (c *converter) convertArrayType(v *ast.ArrayType) {
	if v == nil {
		return
	}

	c.convertExpr(v.Len)
	c.convertExpr(v.Elt)
}

func (c *converter) convertChanType(v *ast.ChanType) {
	if v == nil {
		return
	}

	c.convertExpr(v.Value)
}

func (c *converter) convertCallExpr(v *ast.CallExpr) {
	if v == nil {
		return
	}
	c.convertExpr(v.Fun)
	for _, v := range v.Args {
		c.convertExpr(v)
	}
}

func (c *converter) convertStarExpr(v *ast.StarExpr) {
	if v == nil {
		return
	}

	c.convertExpr(v.X)
}

func (c *converter) convertSelectorExpr(v *ast.SelectorExpr) {
	if v == nil {
		return
	}

	c.convertExpr(v.X)
	c.convertIdent(v.Sel)
}

func (c *converter) convertParenExpr(v *ast.ParenExpr) {
	if v == nil {
		return
	}

	c.convertExpr(v.X)
}

func (c *converter) convertTypeAssertExpr(v *ast.TypeAssertExpr) {
	if v == nil {
		return
	}

	c.convertExpr(v.X)
	c.convertExpr(v.Type)
}

func (c *converter) convertMapType(v *ast.MapType) {
	if v == nil {
		return
	}

	c.convertExpr(v.Key)
	c.convertExpr(v.Value)
}

func (c *converter) convertSliceExpr(v *ast.SliceExpr) {
	if v == nil {
		return
	}

	c.convertExpr(v.X)
	c.convertExpr(v.Low)
	c.convertExpr(v.High)
	c.convertExpr(v.Max)
}

func (c *converter) convertKeyValueExpr(v *ast.KeyValueExpr) {
	if v == nil {
		return
	}

	c.convertExpr(v.Key)
	c.convertExpr(v.Value)
}

func (c *converter) convertExprStmt(v *ast.ExprStmt) {
	if v == nil {
		return
	}

	c.convertExpr(v.X)
}

func (c *converter) convertOptionalType(v *ast.OptionalType) {
	if v == nil {
		return
	}
	c.putChunks(int(v.Pos()), c.src[c.lastChunkEnd:int(v.Pos())-1-c.base])
	c.convertExpr(v.Elt)
}

func (c *converter) convertUnaryExpr(v *ast.UnaryExpr) {
	if v == nil {
		return
	}

	c.convertExpr(v.X)
}

func (c *converter) convertBinaryExpr(v *ast.BinaryExpr) {
	if v == nil {
		return
	}

	c.convertExpr(v.X)
	c.convertExpr(v.Y)
}

func (c *converter) convertCompositeLit(v *ast.CompositeLit) {
	if v == nil {
		return
	}
	c.convertExpr(v.Type)
	for _, v := range v.Elts {
		c.convertExpr(v)
	}
}

func (c *converter) convertStructType(v *ast.StructType) {
	if v == nil {
		return
	}

	c.convertFieldList(v.Fields)
}

func (c *converter) convertFieldList(v *ast.FieldList) {
	if v == nil {
		return
	}
	for _, v := range v.List {
		c.convertField(v)
	}
	if v.Entangled != nil {
		entangledEnd := int(v.List[len(v.List)-1].End()-1) - c.base
		c.putChunks(int(v.Entangled.Pos()-1), c.src[c.lastChunkEnd:entangledEnd], []byte{',', ' '})
		c.convertField(v.Entangled)
	}
}

func (c *converter) convertField(v *ast.Field) {
	if v == nil {
		return
	}
	for _, v := range v.Names {
		c.convertIdent(v)
	}
	c.convertExpr(v.Type)
	c.convertBasicLit(v.Tag)
}

func (c *converter) convertBasicLit(v *ast.BasicLit) {
	if v == nil {
		return
	}

}

func (c *converter) convertIdent(v *ast.Ident) {
	if v == nil {
		return
	}
}

func (c *converter) convertFuncLit(v *ast.FuncLit) {
	if v == nil {
		return
	}
	unset := c.setLastFunc(c.Info.TypeOf(v).(*types.Signature), v.Type)
	defer unset()
	c.convertFuncType(v.Type)
	c.convertBlockStmt(v.Body)
}

func (c *converter) convertInterfaceType(v *ast.InterfaceType) {
	if v == nil {
		return
	}
	c.convertFieldList(v.Methods)
}

func (c *converter) putChunks(newEnd int, chunks ...[]byte) {
	c.dstChunks = append(c.dstChunks, chunks...)
	c.lastChunkEnd = newEnd - c.base
}
