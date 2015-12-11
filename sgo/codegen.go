package sgo

import (
	"fmt"
	goast "go/ast"
	"go/printer"
	gotoken "go/token"
	"io"
	"io/ioutil"

	"github.com/tcard/sgo/sgo/ast"
	"github.com/tcard/sgo/sgo/importer"
	"github.com/tcard/sgo/sgo/parser"
	"github.com/tcard/sgo/sgo/scanner"
	"github.com/tcard/sgo/sgo/token"
	"github.com/tcard/sgo/sgo/types"
)

func TranslateFile(w io.Writer, r io.Reader, filename string) error {
	src, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	fset := &token.FileSet{}
	fset.AddFile(filename, 0, len(src))
	// TODO: parser.ParseComments, but tracking new positions. It breaks bad
	// with newly generated code.
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return err
	}

	gen, errs := Translate("translate", fset, file)
	if len(errs) > 0 {
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

	gofset := &gotoken.FileSet{}
	err = printer.Fprint(w, gofset, gen[0])
	if err != nil {
		return err
	}

	return nil
}

func Translate(path string, fset *token.FileSet, sgoFiles ...*ast.File) ([]*goast.File, []error) {
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

	var goFiles []*goast.File
	for _, sgoFile := range sgoFiles {
		goFiles = append(goFiles, convertAST(info, sgoFile))
	}

	return goFiles, nil
}

func convertAST(info *types.Info, sgoAST *ast.File) *goast.File {
	c := converter{Info: info}
	return c.convertFile(sgoAST)
}

type converter struct {
	*types.Info
	lastFunc *types.Signature
}

func (c *converter) convertFile(v *ast.File) *goast.File {
	if v == nil {
		return nil
	}
	var decls []goast.Decl
	for _, v := range v.Decls {
		decls = append(decls, c.convertDecl(v))
	}
	var comments []*goast.CommentGroup
	for _, v := range v.Comments {
		comments = append(comments, c.convertCommentGroup(v))
	}
	return &goast.File{
		Doc:      c.convertCommentGroup(v.Doc),
		Name:     c.convertIdent(v.Name),
		Decls:    decls,
		Comments: comments,
	}
}

func (c *converter) convertCommentGroup(v *ast.CommentGroup) *goast.CommentGroup {
	if v == nil {
		return nil
	}
	var list []*goast.Comment
	for _, v := range v.List {
		list = append(list, c.convertComment(v))
	}
	return &goast.CommentGroup{
		List: list,
	}
}

func (c *converter) convertComment(v *ast.Comment) *goast.Comment {
	if v == nil {
		return nil
	}
	return &goast.Comment{
		Slash: gotoken.Pos(v.Slash),
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
	default:
		panic(fmt.Sprintf("unhandled Decl %T", v))
	}
}

func (c *converter) convertGenDecl(v *ast.GenDecl) *goast.GenDecl {
	if v == nil {
		return nil
	}
	var specs []goast.Spec
	for _, v := range v.Specs {
		specs = append(specs, c.convertSpec(v))
	}
	return &goast.GenDecl{
		Doc:    c.convertCommentGroup(v.Doc),
		TokPos: gotoken.Pos(v.TokPos),
		Tok:    c.convertToken(v.Tok),
		Lparen: gotoken.Pos(v.Lparen),
		Specs:  specs,
		Rparen: gotoken.Pos(v.Rparen),
	}
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
		EndPos:  gotoken.Pos(v.Pos()),
	}
}

func (c *converter) convertValueSpec(v *ast.ValueSpec) *goast.ValueSpec {
	if v == nil {
		return nil
	}
	var names []*goast.Ident
	for _, name := range v.Names {
		names = append(names, c.convertIdent(name))
	}
	return &goast.ValueSpec{
		Doc:     c.convertCommentGroup(v.Doc),
		Names:   names,
		Type:    c.convertExpr(v.Type),
		Values:  c.convertExprList(v.Values),
		Comment: c.convertCommentGroup(v.Comment),
	}
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
	c.lastFunc = c.Info.ObjectOf(v.Name).Type().(*types.Signature)
	return &goast.FuncDecl{
		Doc:  c.convertCommentGroup(v.Doc),
		Recv: c.convertFieldList(v.Recv),
		Name: c.convertIdent(v.Name),
		Type: c.convertFuncType(v.Type),
		Body: c.convertBlockStmt(v.Body),
	}
}

func (c *converter) convertFuncType(v *ast.FuncType) *goast.FuncType {
	if v == nil {
		return nil
	}
	return &goast.FuncType{
		Func:    gotoken.Pos(v.Func),
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
	default:
		panic(fmt.Sprintf("unhandled Stmt %T", v))
	}
}

func (c *converter) convertReturnStmt(v *ast.ReturnStmt) *goast.ReturnStmt {
	if v == nil {
		return nil
	}
	var results []goast.Expr
	if v.Results.EntangledPos == 0 {
		// return \ err
		for i := 0; i < c.lastFunc.Results().Len(); i++ {
			typ := c.lastFunc.Results().At(i).Type()
			var e goast.Expr
			switch underlying := typ.Underlying().(type) {
			case *types.Pointer, *types.Map, *types.Slice, *types.Signature, *types.Interface, *types.Optional:
				e = goast.NewIdent("nil")
			case *types.Struct:
				panic("TODO")
			case *types.Basic:
				info := underlying.Info()
				switch {
				case info&types.IsBoolean != 0:
					e = goast.NewIdent("false")
				case info&types.IsInteger != 0:
					e = &goast.BasicLit{Kind: gotoken.INT, Value: "0"}
				case info&types.IsFloat != 0:
					e = &goast.BasicLit{Kind: gotoken.FLOAT, Value: "0.0"}
				case info&types.IsComplex != 0:
					e = &goast.BasicLit{Kind: gotoken.FLOAT, Value: "0.0"}
				case info&types.IsString != 0:
					e = &goast.BasicLit{Kind: gotoken.STRING, Value: `""`}
				default:
					e = goast.NewIdent("nil")
				}
			default:
				panic(fmt.Sprintf("unhandled Type %v", typ))
			}
			results = append(results, e)
		}
	}
	for _, v := range v.Results.List {
		results = append(results, c.convertExpr(v))
	}
	if v.Results.EntangledPos > 0 {
		results = append(results, goast.NewIdent("nil"))
	}
	return &goast.ReturnStmt{
		Return:  gotoken.Pos(v.Return),
		Results: results,
	}
}

func (c *converter) convertAssignStmt(v *ast.AssignStmt) *goast.AssignStmt {
	if v == nil {
		return nil
	}
	var lhs []goast.Expr
	for _, v := range v.Lhs.List {
		lhs = append(lhs, c.convertExpr(v))
	}
	var rhs []goast.Expr
	for _, v := range v.Rhs.List {
		rhs = append(rhs, c.convertExpr(v))
	}
	return &goast.AssignStmt{
		Lhs:    lhs,
		TokPos: gotoken.Pos(v.TokPos),
		Tok:    c.convertToken(v.Tok),
		Rhs:    rhs,
	}
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
	var list []goast.Stmt
	for _, v := range v.List {
		list = append(list, c.convertStmt(v))
	}
	return &goast.BlockStmt{
		Lbrace: gotoken.Pos(v.Lbrace),
		List:   list,
		Rbrace: gotoken.Pos(v.Rbrace),
	}
}

func (c *converter) convertTypeSwitchStmt(v *ast.TypeSwitchStmt) *goast.TypeSwitchStmt {
	if v == nil {
		return nil
	}
	return &goast.TypeSwitchStmt{
		Init:   c.convertStmt(v.Init),
		Assign: c.convertStmt(v.Assign),
		Body:   c.convertBlockStmt(v.Body),
	}
}

func (c *converter) convertCaseClause(v *ast.CaseClause) *goast.CaseClause {
	if v == nil {
		return nil
	}
	var list []goast.Expr
	for _, v := range v.List.List {
		list = append(list, c.convertExpr(v))
	}
	var body []goast.Stmt
	for _, v := range v.Body {
		body = append(body, c.convertStmt(v))
	}
	return &goast.CaseClause{
		List: list,
		Body: body,
	}
}

func (c *converter) convertIfStmt(v *ast.IfStmt) *goast.IfStmt {
	if v == nil {
		return nil
	}
	return &goast.IfStmt{
		If:   gotoken.Pos(v.If),
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
	default:
		panic(fmt.Sprintf("unhandled Expr %T", v))
	}
}

func (c *converter) convertCallExpr(v *ast.CallExpr) *goast.CallExpr {
	if v == nil {
		return nil
	}
	var args []goast.Expr
	for _, v := range v.Args {
		args = append(args, c.convertExpr(v))
	}
	return &goast.CallExpr{
		Fun:      c.convertExpr(v.Fun),
		Lparen:   gotoken.Pos(v.Lparen),
		Args:     args,
		Ellipsis: gotoken.Pos(v.Ellipsis),
		Rparen:   gotoken.Pos(v.Rparen),
	}
}

func (c *converter) convertStarExpr(v *ast.StarExpr) *goast.StarExpr {
	if v == nil {
		return nil
	}
	return &goast.StarExpr{
		Star: gotoken.Pos(v.Star),
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
		// Left
		X: c.convertExpr(v.X),
		// Right
	}
}

func (c *converter) convertTypeAssertExpr(v *ast.TypeAssertExpr) *goast.TypeAssertExpr {
	if v == nil {
		return nil
	}
	return &goast.TypeAssertExpr{
		X:    c.convertExpr(v.X),
		Type: c.convertExpr(v.Type),
	}
}

func (c *converter) convertMapType(v *ast.MapType) *goast.MapType {
	if v == nil {
		return nil
	}
	return &goast.MapType{
		Key:   c.convertExpr(v.Key),
		Value: c.convertExpr(v.Value),
	}
}

func (c *converter) convertIndexExpr(v *ast.IndexExpr) *goast.IndexExpr {
	if v == nil {
		return nil
	}
	return &goast.IndexExpr{
		X:     c.convertExpr(v.X),
		Index: c.convertExpr(v.Index),
	}
}

func (c *converter) convertKeyValueExpr(v *ast.KeyValueExpr) *goast.KeyValueExpr {
	if v == nil {
		return nil
	}
	return &goast.KeyValueExpr{
		Key:   c.convertExpr(v.Key),
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
		OpPos: gotoken.Pos(v.OpPos),
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
		OpPos: gotoken.Pos(v.OpPos),
		Op:    c.convertToken(v.Op),
		Y:     c.convertExpr(v.Y),
	}
}

func (c *converter) convertCompositeLit(v *ast.CompositeLit) *goast.CompositeLit {
	if v == nil {
		return nil
	}
	var elts []goast.Expr
	for _, v := range v.Elts {
		elts = append(elts, c.convertExpr(v))
	}
	return &goast.CompositeLit{
		Type:   c.convertExpr(v.Type),
		Lbrace: gotoken.Pos(v.Lbrace),
		Elts:   elts,
		Rbrace: gotoken.Pos(v.Rbrace),
	}
}

func (c *converter) convertStructType(v *ast.StructType) *goast.StructType {
	if v == nil {
		return nil
	}
	return &goast.StructType{
		Struct:     gotoken.Pos(v.Struct),
		Fields:     c.convertFieldList(v.Fields),
		Incomplete: v.Incomplete,
	}
}

func (c *converter) convertFieldList(v *ast.FieldList) *goast.FieldList {
	if v == nil {
		return nil
	}
	var list []*goast.Field
	for _, v := range v.List {
		list = append(list, c.convertField(v))
	}
	if v.Entangled != nil {
		list = append(list, c.convertField(v.Entangled))
	}
	return &goast.FieldList{
		Opening: gotoken.Pos(v.Opening),
		List:    list,
		Closing: gotoken.Pos(v.Closing),
	}
}

func (c *converter) convertField(v *ast.Field) *goast.Field {
	if v == nil {
		return nil
	}
	var names []*goast.Ident
	for _, v := range v.Names {
		names = append(names, c.convertIdent(v))
	}
	return &goast.Field{
		Doc:     c.convertCommentGroup(v.Doc),
		Names:   names,
		Type:    c.convertExpr(v.Type),
		Tag:     c.convertBasicLit(v.Tag),
		Comment: c.convertCommentGroup(v.Comment),
	}
}

func (c *converter) convertBasicLit(v *ast.BasicLit) *goast.BasicLit {
	if v == nil {
		return nil
	}
	return &goast.BasicLit{
		ValuePos: gotoken.Pos(v.ValuePos),
		Kind:     c.convertToken(v.Kind),
		Value:    v.Value,
	}
}

func (c *converter) convertIdent(v *ast.Ident) *goast.Ident {
	if v == nil {
		return nil
	}
	return &goast.Ident{
		NamePos: gotoken.Pos(v.NamePos),
		Name:    v.Name,
	}
}

func (c *converter) convertFuncLit(v *ast.FuncLit) *goast.FuncLit {
	if v == nil {
		return nil
	}
	return &goast.FuncLit{
		Type: c.convertFuncType(v.Type),
		Body: c.convertBlockStmt(v.Body),
	}
}

func (c *converter) convertInterfaceType(v *ast.InterfaceType) *goast.InterfaceType {
	if v == nil {
		return nil
	}
	return &goast.InterfaceType{
		Methods:    c.convertFieldList(v.Methods),
		Incomplete: v.Incomplete,
	}
}

func (c *converter) convertToken(v token.Token) gotoken.Token {
	offset := 0
	if v > token.QUEST {
		offset = 1
	}
	return gotoken.Token(int(v) - offset)
}
