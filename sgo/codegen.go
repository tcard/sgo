package sgo

import (
	"bytes"
	"fmt"
	goast "go/ast"
	goprinter "go/printer"
	gotoken "go/token"
	"io"
	"io/ioutil"

	"github.com/tcard/sgo/sgo/ast"
	"github.com/tcard/sgo/sgo/importer"
	"github.com/tcard/sgo/sgo/parser"
	"github.com/tcard/sgo/sgo/printer"
	"github.com/tcard/sgo/sgo/scanner"
	"github.com/tcard/sgo/sgo/token"
	"github.com/tcard/sgo/sgo/types"
)

func TranslateFile(w io.Writer, r io.Reader, filename string) error {
	src, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return err
	}

	// Early typecheck, because fileWithAnnotationComments messes with line
	// numbers.
	_, errs := typecheck("translate", fset, file)
	if len(errs) > 0 {
		return makeErrList(fset, errs)
	}

	file, err = fileWithAnnotationComments(file, fset, src)
	if err != nil {
		return err
	}

	info, errs := typecheck("translate", fset, file)
	if len(errs) > 0 {
		return makeErrList(fset, errs)
	}

	gofset := gotoken.NewFileSet()

	gen := translate(info, gofset, fset, file)
	err = goprinter.Fprint(w, gofset, gen[0])
	if err != nil {
		return err
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

func typecheck(path string, fset *token.FileSet, sgoFiles ...*ast.File) (*types.Info, []error) {
	var errors []error
	cfg := &types.Config{
		Error: func(err error) {
			errors = append(errors, err)
		},
		Importer: importer.Default(),
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
	_, err := cfg.Check(path, fset, sgoFiles, info)
	if err != nil {
		return nil, errors
	}
	return info, nil
}

func translate(info *types.Info, gofset *gotoken.FileSet, fset *token.FileSet, sgoFiles ...*ast.File) []*goast.File {
	var goFiles []*goast.File
	for _, sgoFile := range sgoFiles {
		fsetFile := fset.File(sgoFile.Pos())
		goFile := convertAST(gofset, fsetFile, info, sgoFile)
		tokenFile := gofset.AddFile(goFile.Name.Name, -1, int(goFile.End()))
		for _, com := range goFile.Comments {
			// Yeah, I don't know why this works, but it does.
			tokenFile.AddLine(int(com.Pos()) - 1)
		}
		goFiles = append(goFiles, goFile)
	}
	return goFiles
}

func fileWithAnnotationComments(file *ast.File, fset *token.FileSet, src []byte) (*ast.File, error) {
	// TODO: So this is an extremely hacky way of doing this. We're going to
	// add the comments directly to the source comments, as text, and then
	// we're going to re-parse it. This is because I tried manipulating the
	// AST, adding the commments there an shifting the nodes' positions, but
	// doing that right is very very convoluted; you need to be tracking all
	// the time where you are, where you _were_, figure out where's a line
	// break, etc. So, well, this will do for now.
	var err error
	offset := 0
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
		pos := int(node.Pos()) - 1 + offset
		var space []byte
		for i := pos - 1; i >= 0 && (src[i] == ' ' || src[i] == '\t'); i-- {
			space = append([]byte{src[i]}, space...)
		}
		text := append([]byte("// For SGo: "+buf.String()+"\n"), space...)
		src = append(src[:pos], append(text, src[pos:]...)...)
		offset += len(text)
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
		return nil, err
	}

	return parser.ParseFile(fset, file.Name.Name, src, parser.ParseComments)
}

type visitorFunc func(node ast.Node) (w ast.Visitor)

func (v visitorFunc) Visit(node ast.Node) (w ast.Visitor) {
	return v(node)
}

func convertAST(gofset *gotoken.FileSet, fsetFile *token.File, info *types.Info, sgoAST *ast.File) *goast.File {
	c := converter{
		Info:     info,
		gofset:   gofset,
		fsetFile: fsetFile,
		comments: map[*ast.CommentGroup]*goast.CommentGroup{},
	}
	ret := c.convertFile(sgoAST)
	// f := gofset.AddFile(ret.Name.Name, -1, int(ret.End()))
	// f.SetLines(c.lines)
	return ret
}

type converter struct {
	*types.Info
	gofset      *gotoken.FileSet
	fsetFile    *token.File
	lastFunc    *types.Signature
	lastFuncAST *goast.FuncType
	comments    map[*ast.CommentGroup]*goast.CommentGroup
	posOffset   int
	lastLine    int
	lines       []int
}

func (c *converter) convertFile(v *ast.File) *goast.File {
	if v == nil {
		return nil
	}
	ret := &goast.File{}
	ret.Doc = c.convertCommentGroup(v.Doc)
	ret.Package = c.convertPos(v.Package)
	ret.Name = c.convertIdent(v.Name)
	for _, v := range v.Decls {
		ret.Decls = append(ret.Decls, c.convertDecl(v))
	}
	for _, v := range v.Comments {
		if cg, ok := c.comments[v]; ok {
			ret.Comments = append(ret.Comments, cg)
		} else {
			ret.Comments = append(ret.Comments, c.convertCommentGroup(v))
		}
	}
	return ret
}

func (c *converter) convertCommentGroup(v *ast.CommentGroup) *goast.CommentGroup {
	if v == nil {
		return nil
	}
	var list []*goast.Comment
	for _, v := range v.List {
		list = append(list, c.convertComment(v))
	}
	ret := &goast.CommentGroup{
		List: list,
	}
	c.comments[v] = ret
	return ret
}

func (c *converter) convertComment(v *ast.Comment) *goast.Comment {
	if v == nil {
		return nil
	}
	return &goast.Comment{
		Slash: c.convertPos(v.Slash),
		Text:  v.Text,
	}
}

func (c *converter) convertDecl(v ast.Decl) goast.Decl {
	if v == nil {
		return nil
	}
	switch v := v.(type) {
	case *ast.GenDecl:
		return c.convertGenDecl(v)
	case *ast.FuncDecl:
		return c.convertFuncDecl(v)
	case *ast.BadDecl:
		return c.convertBadDecl(v)
	default:
		panic(fmt.Sprintf("unhandled Decl %T", v))
	}
}

func (c *converter) convertBadDecl(v *ast.BadDecl) *goast.BadDecl {
	if v == nil {
		return nil
	}
	return &goast.BadDecl{
		From: c.convertPos(v.From),
		To:   c.convertPos(v.To),
	}
}

func (c *converter) convertGenDecl(v *ast.GenDecl) *goast.GenDecl {
	if v == nil {
		return nil
	}
	ret := &goast.GenDecl{}
	ret.Doc = c.convertCommentGroup(v.Doc)
	ret.TokPos = c.convertPos(v.TokPos)
	ret.Tok = c.convertToken(v.Tok)
	ret.Lparen = c.convertPos(v.Lparen)
	for _, v := range v.Specs {
		ret.Specs = append(ret.Specs, c.convertSpec(v))
	}
	ret.Rparen = c.convertPos(v.Rparen)
	return ret
}

func (c *converter) convertSpec(v ast.Spec) goast.Spec {
	if v == nil {
		return nil
	}
	switch v := v.(type) {
	case *ast.TypeSpec:
		return c.convertTypeSpec(v)
	case *ast.ImportSpec:
		return c.convertImportSpec(v)
	case *ast.ValueSpec:
		return c.convertValueSpec(v)
	default:
		panic(fmt.Sprintf("unhandled Spec %T", v))
	}
}

func (c *converter) convertTypeSpec(v *ast.TypeSpec) *goast.TypeSpec {
	if v == nil {
		return nil
	}
	return &goast.TypeSpec{
		Doc:     c.convertCommentGroup(v.Doc),
		Name:    c.convertIdent(v.Name),
		Type:    c.convertExpr(v.Type),
		Comment: c.convertCommentGroup(v.Comment),
	}
}

func (c *converter) convertImportSpec(v *ast.ImportSpec) *goast.ImportSpec {
	if v == nil {
		return nil
	}
	return &goast.ImportSpec{
		Doc:     c.convertCommentGroup(v.Doc),
		Name:    c.convertIdent(v.Name),
		Path:    c.convertBasicLit(v.Path),
		Comment: c.convertCommentGroup(v.Comment),
		EndPos:  c.convertPos(v.EndPos),
	}
}

func (c *converter) convertValueSpec(v *ast.ValueSpec) *goast.ValueSpec {
	if v == nil {
		return nil
	}
	ret := &goast.ValueSpec{}
	ret.Doc = c.convertCommentGroup(v.Doc)
	for _, name := range v.Names {
		ret.Names = append(ret.Names, c.convertIdent(name))
	}
	ret.Type = c.convertExpr(v.Type)
	ret.Values = c.convertExprList(v.Values)
	ret.Comment = c.convertCommentGroup(v.Comment)
	return ret
}

func (c *converter) convertExprList(v *ast.ExprList) []goast.Expr {
	if v == nil {
		return nil
	}
	var exprs []goast.Expr
	for _, expr := range v.List {
		exprs = append(exprs, c.convertExpr(expr))
	}
	return exprs
}

func (c *converter) convertFuncDecl(v *ast.FuncDecl) *goast.FuncDecl {
	if v == nil {
		return nil
	}
	ret := &goast.FuncDecl{}
	ret.Doc = c.convertCommentGroup(v.Doc)
	ret.Recv = c.convertFieldList(v.Recv)
	ret.Name = c.convertIdent(v.Name)
	typ, unset := c.setLastFunc(c.Info.ObjectOf(v.Name).Type().(*types.Signature), v.Type)
	defer unset()
	ret.Type = typ
	ret.Body = c.convertBlockStmt(v.Body)
	return ret
}

func (c *converter) setLastFunc(sig *types.Signature, astTyp *ast.FuncType) (*goast.FuncType, func()) {
	oldLastFunc, oldLastFuncAST := c.lastFunc, c.lastFuncAST
	c.lastFunc = sig
	goTyp := c.convertFuncType(astTyp)
	c.lastFuncAST = goTyp
	return goTyp, func() {
		c.lastFunc, c.lastFuncAST = oldLastFunc, oldLastFuncAST
	}
}

func (c *converter) convertFuncType(v *ast.FuncType) *goast.FuncType {
	if v == nil {
		return nil
	}
	return &goast.FuncType{
		Func:    c.convertPos(v.Func),
		Params:  c.convertFieldList(v.Params),
		Results: c.convertFieldList(v.Results),
	}
}

func (c *converter) convertStmt(v ast.Stmt) goast.Stmt {
	if v == nil {
		return nil
	}
	switch v := v.(type) {
	case *ast.ReturnStmt:
		return c.convertReturnStmt(v)
	case *ast.AssignStmt:
		return c.convertAssignStmt(v)
	case *ast.IfStmt:
		return c.convertIfStmt(v)
	case *ast.ExprStmt:
		return c.convertExprStmt(v)
	case *ast.BlockStmt:
		return c.convertBlockStmt(v)
	case *ast.DeclStmt:
		return c.convertDeclStmt(v)
	case *ast.TypeSwitchStmt:
		return c.convertTypeSwitchStmt(v)
	case *ast.CaseClause:
		return c.convertCaseClause(v)
	case *ast.BadStmt:
		return c.convertBadStmt(v)
	case *ast.BranchStmt:
		return c.convertBranchStmt(v)
	case *ast.CommClause:
		return c.convertCommClause(v)
	case *ast.DeferStmt:
		return c.convertDeferStmt(v)
	case *ast.EmptyStmt:
		return c.convertEmptyStmt(v)
	case *ast.ForStmt:
		return c.convertForStmt(v)
	case *ast.GoStmt:
		return c.convertGoStmt(v)
	case *ast.IncDecStmt:
		return c.convertIncDecStmt(v)
	case *ast.LabeledStmt:
		return c.convertLabeledStmt(v)
	case *ast.RangeStmt:
		return c.convertRangeStmt(v)
	case *ast.SelectStmt:
		return c.convertSelectStmt(v)
	case *ast.SendStmt:
		return c.convertSendStmt(v)
	case *ast.SwitchStmt:
		return c.convertSwitchStmt(v)
	default:
		panic(fmt.Sprintf("unhandled Stmt %T", v))
	}
}

func (c *converter) convertSwitchStmt(v *ast.SwitchStmt) *goast.SwitchStmt {
	if v == nil {
		return nil
	}
	return &goast.SwitchStmt{
		Switch: c.convertPos(v.Switch),
		Init:   c.convertStmt(v.Init),
		Tag:    c.convertExpr(v.Tag),
		Body:   c.convertBlockStmt(v.Body),
	}
}

func (c *converter) convertSendStmt(v *ast.SendStmt) *goast.SendStmt {
	if v == nil {
		return nil
	}
	return &goast.SendStmt{
		Chan:  c.convertExpr(v.Chan),
		Arrow: c.convertPos(v.Arrow),
		Value: c.convertExpr(v.Value),
	}
}

func (c *converter) convertSelectStmt(v *ast.SelectStmt) *goast.SelectStmt {
	if v == nil {
		return nil
	}
	return &goast.SelectStmt{
		Select: c.convertPos(v.Select),
		Body:   c.convertBlockStmt(v.Body),
	}
}

func (c *converter) convertRangeStmt(v *ast.RangeStmt) *goast.RangeStmt {
	if v == nil {
		return nil
	}
	return &goast.RangeStmt{
		For:    c.convertPos(v.For),
		Key:    c.convertExpr(v.Key),
		Value:  c.convertExpr(v.Value),
		TokPos: c.convertPos(v.TokPos),
		Tok:    c.convertToken(v.Tok),
		X:      c.convertExpr(v.X),
		Body:   c.convertBlockStmt(v.Body),
	}
}

func (c *converter) convertLabeledStmt(v *ast.LabeledStmt) *goast.LabeledStmt {
	if v == nil {
		return nil
	}
	return &goast.LabeledStmt{
		Label: c.convertIdent(v.Label),
		Colon: c.convertPos(v.Colon),
		Stmt:  c.convertStmt(v.Stmt),
	}
}

func (c *converter) convertIncDecStmt(v *ast.IncDecStmt) *goast.IncDecStmt {
	if v == nil {
		return nil
	}
	return &goast.IncDecStmt{
		X:      c.convertExpr(v.X),
		TokPos: c.convertPos(v.TokPos),
		Tok:    c.convertToken(v.Tok),
	}
}

func (c *converter) convertForStmt(v *ast.ForStmt) *goast.ForStmt {
	if v == nil {
		return nil
	}
	return &goast.ForStmt{
		For:  c.convertPos(v.For),
		Init: c.convertStmt(v.Init),
		Cond: c.convertExpr(v.Cond),
		Post: c.convertStmt(v.Post),
		Body: c.convertBlockStmt(v.Body),
	}
}

func (c *converter) convertEmptyStmt(v *ast.EmptyStmt) *goast.EmptyStmt {
	if v == nil {
		return nil
	}
	return &goast.EmptyStmt{
		Semicolon: c.convertPos(v.Semicolon),
		Implicit:  v.Implicit,
	}
}

func (c *converter) convertDeferStmt(v *ast.DeferStmt) *goast.DeferStmt {
	if v == nil {
		return nil
	}
	return &goast.DeferStmt{
		Defer: c.convertPos(v.Defer),
		Call:  c.convertCallExpr(v.Call),
	}
}

func (c *converter) convertGoStmt(v *ast.GoStmt) *goast.GoStmt {
	if v == nil {
		return nil
	}
	return &goast.GoStmt{
		Go:   c.convertPos(v.Go),
		Call: c.convertCallExpr(v.Call),
	}
}

func (c *converter) convertBranchStmt(v *ast.BranchStmt) *goast.BranchStmt {
	if v == nil {
		return nil
	}
	return &goast.BranchStmt{
		TokPos: c.convertPos(v.TokPos),
		Tok:    c.convertToken(v.Tok),
		Label:  c.convertIdent(v.Label),
	}
}

func (c *converter) convertCommClause(v *ast.CommClause) *goast.CommClause {
	if v == nil {
		return nil
	}
	ret := &goast.CommClause{}
	ret.Case = c.convertPos(v.Case)
	ret.Comm = c.convertStmt(v.Comm)
	ret.Colon = c.convertPos(v.Colon)
	for _, v := range v.Body {
		ret.Body = append(ret.Body, c.convertStmt(v))
	}
	return ret
}

func (c *converter) convertReturnStmt(v *ast.ReturnStmt) *goast.ReturnStmt {
	if v == nil {
		return nil
	}
	ret := &goast.ReturnStmt{}
	ret.Return = c.convertPos(v.Return)
	if v.Results.EntangledPos == 0 {
		// return \ err
		ePos := c.convertPos(v.Results.Pos())
		resultsLen := c.lastFunc.Results().Len()
		for i := 0; i < resultsLen; i++ {
			typ := c.lastFunc.Results().At(i).Type()
			var e goast.Expr
			switch underlying := typ.Underlying().(type) {
			case *types.Pointer, *types.Map, *types.Slice, *types.Signature, *types.Interface, *types.Optional:
				e = c.injectedIdent("nil", ePos)
			case *types.Struct:
				typ := c.lastFuncAST.Results.List[i].Type
				typLen := typ.End() - typ.Pos()
				cl := &goast.CompositeLit{Type: typ}
				cl.Lbrace = ePos + typLen
				cl.Rbrace = cl.Rbrace + 1
				e = cl
			case *types.Basic:
				info := underlying.Info()
				switch {
				case info&types.IsBoolean != 0:
					e = c.injectedIdent("false", ePos)
				case info&types.IsInteger != 0:
					e = c.injectedBasicLit(gotoken.INT, "0", ePos)
				case info&types.IsFloat != 0, info&types.IsComplex != 0:
					e = c.injectedBasicLit(gotoken.FLOAT, "0.0", ePos)
				case info&types.IsString != 0:
					e = c.injectedBasicLit(gotoken.STRING, `""`, ePos)
				default:
					e = c.injectedIdent("nil", ePos)
				}
			default:
				panic(fmt.Sprintf("unhandled Type %v", typ))
			}
			ret.Results = append(ret.Results, e)
			ePos += e.End() - e.Pos()
			if i < resultsLen-1 {
				ePos += gotoken.Pos(len(", "))
				c.posOffset += len(", ")
			}
		}
	}
	for _, v := range v.Results.List {
		ret.Results = append(ret.Results, c.convertExpr(v))
	}
	if v.Results.EntangledPos > 0 {
		id := "nil"
		if c.lastFunc.Results().Entangled().Type() == types.Typ[types.Bool] {
			id = "true"
		}
		ret.Results = append(ret.Results, c.injectedIdent(id, c.convertPos(v.Results.End())))
	}
	return ret
}

func (c *converter) convertBadStmt(v *ast.BadStmt) *goast.BadStmt {
	if v == nil {
		return nil
	}
	return &goast.BadStmt{
		From: c.convertPos(v.From),
		To:   c.convertPos(v.To),
	}
}

func (c *converter) convertAssignStmt(v *ast.AssignStmt) *goast.AssignStmt {
	if v == nil {
		return nil
	}
	ret := &goast.AssignStmt{}
	for _, v := range v.Lhs.List {
		ret.Lhs = append(ret.Lhs, c.convertExpr(v))
	}
	ret.TokPos = c.convertPos(v.TokPos)
	ret.Tok = c.convertToken(v.Tok)
	for _, v := range v.Rhs.List {
		ret.Rhs = append(ret.Rhs, c.convertExpr(v))
	}
	return ret
}

func (c *converter) convertDeclStmt(v *ast.DeclStmt) *goast.DeclStmt {
	if v == nil {
		return nil
	}
	return &goast.DeclStmt{
		Decl: c.convertDecl(v.Decl),
	}
}

func (c *converter) convertBlockStmt(v *ast.BlockStmt) *goast.BlockStmt {
	if v == nil {
		return nil
	}
	ret := &goast.BlockStmt{}
	ret.Lbrace = c.convertPos(v.Lbrace)
	for _, v := range v.List {
		ret.List = append(ret.List, c.convertStmt(v))
	}
	ret.Rbrace = c.convertPos(v.Rbrace)
	return ret
}

func (c *converter) convertTypeSwitchStmt(v *ast.TypeSwitchStmt) *goast.TypeSwitchStmt {
	if v == nil {
		return nil
	}
	return &goast.TypeSwitchStmt{
		Switch: c.convertPos(v.Switch),
		Init:   c.convertStmt(v.Init),
		Assign: c.convertStmt(v.Assign),
		Body:   c.convertBlockStmt(v.Body),
	}
}

func (c *converter) convertCaseClause(v *ast.CaseClause) *goast.CaseClause {
	if v == nil {
		return nil
	}
	ret := &goast.CaseClause{}
	ret.Case = c.convertPos(v.Case)
	for _, v := range v.List.List {
		ret.List = append(ret.List, c.convertExpr(v))
	}
	ret.Colon = c.convertPos(v.Colon)
	for _, v := range v.Body {
		ret.Body = append(ret.Body, c.convertStmt(v))
	}
	return ret
}

func (c *converter) convertIfStmt(v *ast.IfStmt) *goast.IfStmt {
	if v == nil {
		return nil
	}
	return &goast.IfStmt{
		If:   c.convertPos(v.If),
		Init: c.convertStmt(v.Init),
		Cond: c.convertExpr(v.Cond),
		Body: c.convertBlockStmt(v.Body),
		Else: c.convertStmt(v.Else),
	}
}

func (c *converter) convertExpr(v ast.Expr) goast.Expr {
	if v == nil {
		return nil
	}
	switch v := v.(type) {
	case *ast.StructType:
		return c.convertStructType(v)
	case *ast.Ident:
		return c.convertIdent(v)
	case *ast.CallExpr:
		return c.convertCallExpr(v)
	case *ast.StarExpr:
		return c.convertStarExpr(v)
	case *ast.OptionalType:
		return c.convertOptionalType(v)
	case *ast.CompositeLit:
		return c.convertCompositeLit(v)
	case *ast.UnaryExpr:
		return c.convertUnaryExpr(v)
	case *ast.BasicLit:
		return c.convertBasicLit(v)
	case *ast.BinaryExpr:
		return c.convertBinaryExpr(v)
	case *ast.SelectorExpr:
		return c.convertSelectorExpr(v)
	case *ast.FuncType:
		return c.convertFuncType(v)
	case *ast.FuncLit:
		return c.convertFuncLit(v)
	case *ast.InterfaceType:
		return c.convertInterfaceType(v)
	case *ast.ParenExpr:
		return c.convertParenExpr(v)
	case *ast.TypeAssertExpr:
		return c.convertTypeAssertExpr(v)
	case *ast.MapType:
		return c.convertMapType(v)
	case *ast.IndexExpr:
		return c.convertIndexExpr(v)
	case *ast.KeyValueExpr:
		return c.convertKeyValueExpr(v)
	case *ast.ArrayType:
		return c.convertArrayType(v)
	case *ast.BadExpr:
		return c.convertBadExpr(v)
	case *ast.ChanType:
		return c.convertChanType(v)
	case *ast.Ellipsis:
		return c.convertEllipsis(v)
	case *ast.SliceExpr:
		return c.convertSliceExpr(v)
	default:
		panic(fmt.Sprintf("unhandled Expr %T", v))
	}
}

func (c *converter) convertIndexExpr(v *ast.IndexExpr) *goast.IndexExpr {
	if v == nil {
		return nil
	}
	return &goast.IndexExpr{
		X:      c.convertExpr(v.X),
		Lbrack: c.convertPos(v.Lbrack),
		Index:  c.convertExpr(v.Index),
		Rbrack: c.convertPos(v.Rbrack),
	}
}

func (c *converter) convertEllipsis(v *ast.Ellipsis) *goast.Ellipsis {
	if v == nil {
		return nil
	}
	return &goast.Ellipsis{
		Ellipsis: c.convertPos(v.Ellipsis),
		Elt:      c.convertExpr(v.Elt),
	}
}

func (c *converter) convertBadExpr(v *ast.BadExpr) *goast.BadExpr {
	if v == nil {
		return nil
	}
	return &goast.BadExpr{
		From: c.convertPos(v.From),
		To:   c.convertPos(v.To),
	}
}

func (c *converter) convertArrayType(v *ast.ArrayType) *goast.ArrayType {
	if v == nil {
		return nil
	}
	return &goast.ArrayType{
		Lbrack: c.convertPos(v.Lbrack),
		Len:    c.convertExpr(v.Len),
		Elt:    c.convertExpr(v.Elt),
	}
}

func (c *converter) convertChanType(v *ast.ChanType) *goast.ChanType {
	if v == nil {
		return nil
	}
	return &goast.ChanType{
		Begin: c.convertPos(v.Begin),
		Arrow: c.convertPos(v.Arrow),
		Dir:   goast.ChanDir(v.Dir),
		Value: c.convertExpr(v.Value),
	}
}

func (c *converter) convertCallExpr(v *ast.CallExpr) *goast.CallExpr {
	if v == nil {
		return nil
	}
	ret := &goast.CallExpr{}
	ret.Fun = c.convertExpr(v.Fun)
	ret.Lparen = c.convertPos(v.Lparen)
	for _, v := range v.Args {
		ret.Args = append(ret.Args, c.convertExpr(v))
	}
	ret.Ellipsis = c.convertPos(v.Ellipsis)
	ret.Rparen = c.convertPos(v.Rparen)
	return ret
}

func (c *converter) convertStarExpr(v *ast.StarExpr) *goast.StarExpr {
	if v == nil {
		return nil
	}
	return &goast.StarExpr{
		Star: c.convertPos(v.Star),
		X:    c.convertExpr(v.X),
	}
}

func (c *converter) convertSelectorExpr(v *ast.SelectorExpr) *goast.SelectorExpr {
	if v == nil {
		return nil
	}
	return &goast.SelectorExpr{
		X:   c.convertExpr(v.X),
		Sel: c.convertIdent(v.Sel),
	}
}

func (c *converter) convertParenExpr(v *ast.ParenExpr) *goast.ParenExpr {
	if v == nil {
		return nil
	}
	return &goast.ParenExpr{
		Lparen: c.convertPos(v.Lparen),
		X:      c.convertExpr(v.X),
		Rparen: c.convertPos(v.Rparen),
	}
}

func (c *converter) convertTypeAssertExpr(v *ast.TypeAssertExpr) *goast.TypeAssertExpr {
	if v == nil {
		return nil
	}
	return &goast.TypeAssertExpr{
		X:      c.convertExpr(v.X),
		Lparen: c.convertPos(v.Lparen),
		Type:   c.convertExpr(v.Type),
		Rparen: c.convertPos(v.Rparen),
	}
}

func (c *converter) convertMapType(v *ast.MapType) *goast.MapType {
	if v == nil {
		return nil
	}
	return &goast.MapType{
		Map:   c.convertPos(v.Map),
		Key:   c.convertExpr(v.Key),
		Value: c.convertExpr(v.Value),
	}
}

func (c *converter) convertSliceExpr(v *ast.SliceExpr) *goast.SliceExpr {
	if v == nil {
		return nil
	}
	return &goast.SliceExpr{
		X:      c.convertExpr(v.X),
		Lbrack: c.convertPos(v.Lbrack),
		Low:    c.convertExpr(v.Low),
		High:   c.convertExpr(v.High),
		Max:    c.convertExpr(v.Max),
		Slice3: v.Slice3,
		Rbrack: c.convertPos(v.Rbrack),
	}
}

func (c *converter) convertKeyValueExpr(v *ast.KeyValueExpr) *goast.KeyValueExpr {
	if v == nil {
		return nil
	}
	return &goast.KeyValueExpr{
		Key:   c.convertExpr(v.Key),
		Colon: c.convertPos(v.Colon),
		Value: c.convertExpr(v.Value),
	}
}

func (c *converter) convertExprStmt(v *ast.ExprStmt) *goast.ExprStmt {
	if v == nil {
		return nil
	}
	return &goast.ExprStmt{
		X: c.convertExpr(v.X),
	}
}

func (c *converter) convertOptionalType(v *ast.OptionalType) goast.Expr {
	if v == nil {
		return nil
	}
	return c.convertExpr(v.Elt)
}

func (c *converter) convertUnaryExpr(v *ast.UnaryExpr) *goast.UnaryExpr {
	if v == nil {
		return nil
	}
	return &goast.UnaryExpr{
		OpPos: c.convertPos(v.OpPos),
		Op:    c.convertToken(v.Op),
		X:     c.convertExpr(v.X),
	}
}

func (c *converter) convertBinaryExpr(v *ast.BinaryExpr) *goast.BinaryExpr {
	if v == nil {
		return nil
	}
	return &goast.BinaryExpr{
		X:     c.convertExpr(v.X),
		OpPos: c.convertPos(v.OpPos),
		Op:    c.convertToken(v.Op),
		Y:     c.convertExpr(v.Y),
	}
}

func (c *converter) convertCompositeLit(v *ast.CompositeLit) *goast.CompositeLit {
	if v == nil {
		return nil
	}
	ret := &goast.CompositeLit{}
	ret.Type = c.convertExpr(v.Type)
	ret.Lbrace = c.convertPos(v.Lbrace)
	for _, v := range v.Elts {
		ret.Elts = append(ret.Elts, c.convertExpr(v))
	}
	ret.Rbrace = c.convertPos(v.Rbrace)
	return ret
}

func (c *converter) convertStructType(v *ast.StructType) *goast.StructType {
	if v == nil {
		return nil
	}
	return &goast.StructType{
		Struct:     c.convertPos(v.Struct),
		Fields:     c.convertFieldList(v.Fields),
		Incomplete: v.Incomplete,
	}
}

func (c *converter) convertFieldList(v *ast.FieldList) *goast.FieldList {
	if v == nil {
		return nil
	}
	ret := &goast.FieldList{}
	ret.Opening = c.convertPos(v.Opening)
	for _, v := range v.List {
		ret.List = append(ret.List, c.convertField(v))
	}
	if v.Entangled != nil {
		ret.List = append(ret.List, c.convertField(v.Entangled))
	}
	ret.Closing = c.convertPos(v.Closing)
	return ret
}

func (c *converter) convertField(v *ast.Field) *goast.Field {
	if v == nil {
		return nil
	}
	ret := &goast.Field{}
	ret.Doc = c.convertCommentGroup(v.Doc)
	for _, v := range v.Names {
		ret.Names = append(ret.Names, c.convertIdent(v))
	}
	ret.Type = c.convertExpr(v.Type)
	ret.Tag = c.convertBasicLit(v.Tag)
	ret.Comment = c.convertCommentGroup(v.Comment)
	return ret
}

func (c *converter) convertBasicLit(v *ast.BasicLit) *goast.BasicLit {
	if v == nil {
		return nil
	}
	return &goast.BasicLit{
		ValuePos: c.convertPos(v.ValuePos),
		Kind:     c.convertToken(v.Kind),
		Value:    v.Value,
	}
}

func (c *converter) convertIdent(v *ast.Ident) *goast.Ident {
	if v == nil {
		return nil
	}
	return &goast.Ident{
		NamePos: c.convertPos(v.NamePos),
		Name:    v.Name,
	}
}

func (c *converter) convertFuncLit(v *ast.FuncLit) *goast.FuncLit {
	if v == nil {
		return nil
	}
	typ, unset := c.setLastFunc(c.Info.TypeOf(v).(*types.Signature), v.Type)
	defer unset()
	return &goast.FuncLit{
		Type: typ,
		Body: c.convertBlockStmt(v.Body),
	}
}

func (c *converter) convertInterfaceType(v *ast.InterfaceType) *goast.InterfaceType {
	if v == nil {
		return nil
	}
	return &goast.InterfaceType{
		Interface:  c.convertPos(v.Interface),
		Methods:    c.convertFieldList(v.Methods),
		Incomplete: v.Incomplete,
	}
}

func (c *converter) convertToken(v token.Token) gotoken.Token {
	offset := 0
	if v > token.QUEST {
		offset = 1
		c.posOffset--
	}
	return gotoken.Token(int(v) - offset)
}

func (c *converter) convertPos(v token.Pos) gotoken.Pos {
	if v == 0 {
		return 0
	}
	line := c.fsetFile.Line(v)
	newLines := line - c.lastLine
	c.lastLine = line
	ret := gotoken.Pos(int(v) + c.posOffset)
	if newLines > 0 {
		for i := int(ret) - (newLines - 1); i <= int(ret); i++ {
			c.lines = append(c.lines, i)
		}
	}
	return ret
}

func (c *converter) injectedIdent(s string, pos gotoken.Pos) *goast.Ident {
	ret := goast.NewIdent(s)
	ret.NamePos = pos
	c.posOffset += len(s)
	return ret
}

func (c *converter) injectedBasicLit(kind gotoken.Token, value string, pos gotoken.Pos) *goast.BasicLit {
	ret := &goast.BasicLit{Kind: kind, Value: value, ValuePos: pos}
	c.posOffset += len(value)
	return ret
}
