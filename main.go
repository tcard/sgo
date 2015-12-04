package main

import (
	"fmt"
	"io"
	"os"

	"github.com/tcard/sgo/sgo"
	"github.com/tcard/sgo/sgo/scanner"
)

func main() {
	var r io.Reader
	var fileName string
	if len(os.Args) > 1 {
		var err error
		fileName = os.Args[1]
		r, err = os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else {
		r = os.Stdin
		fileName = "stdin.sgo"
	}
	err := sgo.TranslateFile(os.Stdout, r, fileName)
	if err != nil {
		if errs, ok := err.(scanner.ErrorList); ok {
			for _, err := range errs {
				fmt.Fprintln(os.Stderr, err)
			}
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
