package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/tcard/sgo/sgo"
)

func main() {
	err := sgo.TranslateFile(os.Stdout, strings.NewReader(src), "test.sgo")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var src = `
package main

type Result struct {
	a int
}

type Err string

// Error returns the error string
func (e Err) Error() string { return string(e) }

func Foo(i int) (*Result, ?error) {
	if i % 2 == 0 {
		return &Result{i}, nil
	}
	// return nil, Err("hola") -- doesn't compile
	// return nil, nil         -- doesn't compile
	return &Result{i}, Err("hola")
}

func main() {
	a, b := Foo(123)
	if b == nil {
		println(b)
	} else {
		println("HEY", b)
	}
	println(a, b)
}
`
