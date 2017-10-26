package annotations

import (
	"io"
	"testing"
)

func TestTokenizerSkipWhite(t *testing.T) {
	type testCase struct {
		input                       string
		line, col, bytePos, runePos int
	}
	cases := []testCase{
		{
			input: "  end",
			line:  1, col: 3, bytePos: 2, runePos: 2,
		},
		{
			input: " \n\t end",
			line:  2, col: 3, bytePos: 4, runePos: 4,
		},
		{
			input: "end",
			line:  1, col: 1, bytePos: 0, runePos: 0,
		},
		{
			input: " ",
			line:  1, col: 2, bytePos: 1, runePos: 1,
		},
		{
			input: "",
			line:  1, col: 1, bytePos: 0, runePos: 0,
		},
	}

	for i, c := range cases {
		tkr := NewTokenizer(c.input)
		tkr.SkipWhite()
		if tkr.line != c.line {
			t.Errorf("case %d: line: expected %d, got %d", i, c.line, tkr.line)
		}
		if tkr.col() != c.col {
			t.Errorf("case %d: col: expected %d, got %d", i, c.col, tkr.col())
		}
		if tkr.bytePos != c.bytePos {
			t.Errorf("case %d: bytePos: expected %d, got %d", i, c.bytePos, tkr.bytePos)
		}
		if tkr.runePos != c.runePos {
			t.Errorf("case %d: runePos: expected %d, got %d", i, c.runePos, tkr.runePos)
		}
	}
}

type parseTestCase struct {
	pre                                     string
	input                                   string
	r                                       rune
	line, col, bytePos, runePos             int
	newLine, newCol, newBytePos, newRunePos int
	checkErr                                func(*testing.T, int, parseTestCase, error)
}

var parseTestCases = []parseTestCase{
	{
		pre:   "ñandú",
		input: "text",
		r:     't',
		line:  1, col: 6, bytePos: 7, runePos: 5,
		newLine: 1, newCol: 7, newBytePos: 8, newRunePos: 6,
	},
	{
		pre:   "ñand\nú",
		input: "ø",
		r:     'ø',
		line:  2, col: 2, bytePos: 8, runePos: 6,
		newLine: 2, newCol: 3, newBytePos: 10, newRunePos: 7,
	},
	{
		pre:   "ñand\nú",
		input: "\n",
		r:     '\n',
		line:  2, col: 2, bytePos: 8, runePos: 6,
		newLine: 3, newCol: 1, newBytePos: 9, newRunePos: 7,
	},
	{
		pre:   "ñand\nú",
		input: "",
		checkErr: func(t *testing.T, i int, c parseTestCase, err error) {
			if err != io.EOF {
				t.Errorf("case %d: error should be io.EOF, got %T: %[2]v", i, err)
				return
			}
		},
		line: 2, col: 2, bytePos: 8, runePos: 6,
		newLine: 2, newCol: 2, newBytePos: 8, newRunePos: 6,
	},
	{
		pre:   "ñand\nú",
		input: string([]byte{0x99, 0xDE, 0xAD, 0xBE, 0xEF}),
		checkErr: func(t *testing.T, i int, c parseTestCase, err error) {
			uerr, ok := err.(UTF8Error)
			if !ok {
				t.Errorf("case %d: error should be UTF8Error, got %T: %[2]v", i, err)
				return
			}
			if uerr.Line != c.line {
				t.Errorf("case %d: error line: expected %d, got %d", i, c.line, uerr.Line)
			}
			if uerr.Col != c.col {
				t.Errorf("case %d: error col: expected %d, got %d", i, c.col, uerr.Col)
			}
		},
		line: 2, col: 2, bytePos: 8, runePos: 6,
		newLine: 2, newCol: 2, newBytePos: 8, newRunePos: 6,
	},
}

func TestTokenizerPeek(t *testing.T) {
	for i, c := range parseTestCases {
		tkr, cont := testTokenizerPre(t, i, c)
		if cont {
			continue
		}

		tk, err := tkr.Peek()

		testTokenizerErr(t, i, c, err)
		if err == nil {
			testTokenizerToken(t, i, c, tk)
		} else { // Check that it doesn't advance.
			_, nextErr := tkr.Peek()
			testTokenizerErr(t, i, c, err)
			if err != nextErr {
				t.Errorf("case %d: expected error %v, got %v", i, err, nextErr)
			}
		}

		if tkr.line != c.line {
			t.Errorf("case %d: Tokenizer line: expected %d, got %d", i, c.line, tkr.line)
		}
		if tkr.col() != c.col {
			t.Errorf("case %d: Tokenizer col: expected %d, got %d", i, c.col, tkr.col())
		}
		if tkr.bytePos != c.bytePos {
			t.Errorf("case %d: Tokenizer bytePos: expected %d, got %d", i, c.bytePos, tkr.bytePos)
		}
		if tkr.runePos != c.runePos {
			t.Errorf("case %d: Tokenizer runePos: expected %d, got %d", i, c.runePos, tkr.runePos)
		}
	}
}

func TestTokenizerNext(t *testing.T) {
	for i, c := range parseTestCases {
		tkr, cont := testTokenizerPre(t, i, c)
		if cont {
			continue
		}

		tk, err := tkr.Next()

		testTokenizerErr(t, i, c, err)
		if err == nil {
			testTokenizerToken(t, i, c, tk)
		} else { // Check that it doesn't advance.
			_, nextErr := tkr.Next()
			testTokenizerErr(t, i, c, err)
			if err != nextErr {
				t.Errorf("case %d: expected error %v, got %v", i, err, nextErr)
			}
		}

		if tkr.line != c.newLine {
			t.Errorf("case %d: Tokenizer line: expected %d, got %d", i, c.newLine, tkr.line)
		}
		if tkr.col() != c.newCol {
			t.Errorf("case %d: Tokenizer col: expected %d, got %d", i, c.newCol, tkr.col())
		}
		if tkr.bytePos != c.newBytePos {
			t.Errorf("case %d: Tokenizer bytePos: expected %d, got %d", i, c.newBytePos, tkr.bytePos)
		}
		if tkr.runePos != c.newRunePos {
			t.Errorf("case %d: Tokenizer runePos: expected %d, got %d", i, c.newRunePos, tkr.runePos)
		}
	}
}

func testTokenizerPre(t *testing.T, i int, c parseTestCase) (tkr *Tokenizer, cont bool) {
	tkr = NewTokenizer(c.pre + c.input)
	for range c.pre {
		_, err := tkr.Next()
		if err != nil {
			t.Errorf("case %d: unexpected error in pre-input: %v", i, err)
			return tkr, true
		}
	}
	return tkr, false
}

func testTokenizerErr(t *testing.T, i int, c parseTestCase, err error) {
	if err != nil {
		if c.checkErr != nil {
			c.checkErr(t, i, c, err)
		} else {
			t.Errorf("case %d: unexpected error: %v", i, err)
		}
	} else if c.checkErr != nil {
		t.Errorf("case %d: expected error, got nil", i)
	}
}

func testTokenizerToken(t *testing.T, i int, c parseTestCase, tk Token) {
	if tk.Lexeme != c.r {
		t.Errorf("case %d: rune: expected %v, got %v", i, c.r, tk.Lexeme)
	}
	if tk.Line != c.line {
		t.Errorf("case %d: token line: expected %d, got %d", i, c.line, tk.Line)
	}
	if tk.Col != c.col {
		t.Errorf("case %d: token col: expected %d, got %d", i, c.col, tk.Col)
	}
	if tk.BytePos != c.bytePos {
		t.Errorf("case %d: token bytePos: expected %d, got %d", i, c.bytePos, tk.BytePos)
	}
	if tk.RunePos != c.runePos {
		t.Errorf("case %d: token runePos: expected %d, got %d", i, c.runePos, tk.RunePos)
	}
}
