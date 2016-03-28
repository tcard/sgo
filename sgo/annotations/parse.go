package annotations

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

func Parse(src string) (*Annotation, error) {
	anns, err := parseList(NewTokenizer(src))
	return NewAnnotation(anns), err
}

// List -> Item*
// Item -> Name Def /[\n;]*/
// Name -> Ident | Receiver
// Receiver -> "(" "*" Ident ")"
// Ident -> (Go identifier)
// Def -> Type | "{" List "}"
// Type -> /[^{][^\n;]*/

func parseList(src *Tokenizer) (map[string]string, error) {
	anns := map[string]string{}
	for {
		src.SkipWhite()
		tk, err := src.Peek()
		if err != nil && err != io.EOF {
			return nil, err
		}

		if err == io.EOF || tk.Lexeme != '(' && tk.Lexeme != '_' && !unicode.IsLetter(tk.Lexeme) {
			return anns, nil
		}

		itemAnns, err := parseItem(src)
		if err != nil {
			if err == io.EOF {
				return nil, EOF
			}
			return nil, err
		}
		for k, v := range itemAnns {
			anns[k] = v
		}
	}

	return anns, nil
}

func parseItem(src *Tokenizer) (map[string]string, error) {
	name, err := parseName(src)
	if err != nil {
		return nil, err
	}

	src.SkipWhiteUntilLine()
	def, err := parseDef(src)
	if err != nil {
		return nil, err
	}

	src.SkipWhiteUntilLine()
	tk, err := src.Next()
	if err != nil && err != io.EOF {
		return nil, err
	}
	if err != io.EOF && tk.Lexeme != ';' && tk.Lexeme != '\n' {
		return nil, NewUnexpectedTokenError(tk)
	}

	ret := map[string]string{}
	for subItem, subDef := range def {
		k := name
		if subItem != "" {
			k += "." + subItem
		}
		ret[k] = subDef
	}
	return ret, nil
}

func parseName(src *Tokenizer) (string, error) {
	tk, err := src.Peek()
	if err != nil {
		return "", err
	}
	if tk.Lexeme == '(' {
		return parseReceiver(src)
	} else if tk.Lexeme == '_' || unicode.IsLetter(tk.Lexeme) {
		return parseIdent(src)
	} else {
		return "", NewUnexpectedTokenError(tk)
	}
}

func parseReceiver(src *Tokenizer) (string, error) {
	src.Next() // We know it's '('

	src.SkipWhite()
	err := expect('*', src)
	if err != nil {
		return "", err
	}

	src.SkipWhite()
	id, err := parseIdent(src)
	if err != nil {
		return "", err
	}

	src.SkipWhite()
	err = expect(')', src)
	if err != nil {
		return "", err
	}

	return "(*" + id + ")", nil
}

func parseIdent(src *Tokenizer) (string, error) {
	tk, err := src.Next()
	if err != nil {
		return "", err
	}
	id := string(tk.Lexeme)

	for {
		tk, err := src.Peek()
		if err != nil {
			return "", err
		}
		if !unicode.IsLetter(tk.Lexeme) && !unicode.IsDigit(tk.Lexeme) {
			break
		}
		src.Next()
		id += string(tk.Lexeme)
	}

	return id, nil
}

func parseDef(src *Tokenizer) (map[string]string, error) {
	tk, err := src.Peek()
	if err != nil {
		return nil, err
	}

	if tk.Lexeme == '{' {
		src.Next()
		src.SkipWhite()
		anns, err := parseList(src)
		if err != nil {
			return nil, err
		}

		src.SkipWhite()
		err = expect('}', src)
		if err != nil {
			return nil, err
		}

		return anns, nil
	} else {
		typ, err := parseType(src)
		if err != nil {
			return nil, err
		}

		return map[string]string{"": typ}, nil
	}
}

func parseType(src *Tokenizer) (string, error) {
	tk, err := src.Next()
	if err != nil {
		return "", err
	}
	if tk.Lexeme == '{' || tk.Lexeme == '\n' || tk.Lexeme == ';' {
		return "", NewUnexpectedTokenError(tk)
	}
	typ := string(tk.Lexeme)

	for {
		tk, err := src.Peek()
		if err != nil && err != io.EOF {
			return "", err
		}
		if tk.Lexeme == '\n' || tk.Lexeme == ';' || err == io.EOF {
			break
		}
		src.Next()
		typ += string(tk.Lexeme)
	}

	return strings.TrimSpace(typ), nil
}

func expect(r rune, src *Tokenizer) error {
	tk, err := src.Next()
	if err != nil {
		return err
	}
	if tk.Lexeme != r {
		return NewUnexpectedTokenError(tk)
	}
	return nil
}

type Tokenizer struct {
	src         string
	bytePos     int
	runePos     int
	lastLinePos int
	line        int
	lookahead   Token
}

func NewTokenizer(src string) *Tokenizer {
	return &Tokenizer{src: src, line: 1}
}

func (t *Tokenizer) SkipWhite() {
	for {
		tk, err := t.Peek()
		if err != nil || !unicode.IsSpace(tk.Lexeme) {
			return
		}
		t.Next()
	}
}

func (t *Tokenizer) SkipWhiteUntilLine() {
	for {
		tk, err := t.Peek()
		if err != nil || tk.Lexeme == '\n' || !unicode.IsSpace(tk.Lexeme) {
			return
		}
		t.Next()
	}
}

func (t *Tokenizer) empty() bool {
	return t.bytePos >= len(t.src)
}

func (t *Tokenizer) Peek() (Token, error) {
	if t.lookahead.Size > 0 {
		return t.lookahead, nil
	}
	if t.empty() {
		return Token{}, io.EOF
	}
	r, size := utf8.DecodeRuneInString(t.src[t.bytePos:])
	if r == utf8.RuneError {
		return Token{}, NewUTF8Error(t.line, t.col())
	}
	tk := Token{
		Lexeme:  r,
		Size:    size,
		BytePos: t.bytePos,
		RunePos: t.runePos,
		Line:    t.line,
		Col:     t.col(),
	}
	t.lookahead = tk
	return tk, nil
}

func (t *Tokenizer) col() int {
	return t.runePos - t.lastLinePos + 1
}

func (t *Tokenizer) Next() (Token, error) {
	tk, err := t.Peek()
	if err != nil {
		return Token{}, err
	}
	if t.lookahead.Size > 0 {
		t.lookahead = Token{}
	}
	if tk.Size > 0 {
		t.runePos++
		t.bytePos += tk.Size
		if tk.Lexeme == '\n' {
			t.line++
			t.lastLinePos = t.runePos
		}
	}
	return tk, nil
}

type Token struct {
	Lexeme  rune
	Line    int
	Col     int
	Size    int
	BytePos int
	RunePos int
}

type UTF8Error struct {
	Line int
	Col  int
}

func NewUTF8Error(line, col int) UTF8Error {
	return UTF8Error{line, col}
}

func (err UTF8Error) Error() string {
	return fmt.Sprintf("invalid UTF-8 character starting at %d:%d", err.Line, err.Col)
}

type UnexpectedTokenError struct {
	Token Token
}

func NewUnexpectedTokenError(tk Token) UnexpectedTokenError {
	return UnexpectedTokenError{tk}
}

func (err UnexpectedTokenError) Error() string {
	return fmt.Sprintf("unexpected token at %d:%d: '%v'", err.Token.Line, err.Token.Col, string(err.Token.Lexeme))
}

var EOF error = errors.New("unexpected end of file")
