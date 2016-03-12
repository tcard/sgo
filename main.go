package main

import (
	"fmt"
	"io"
	"os"

	"go/scanner"

	"github.com/tcard/sgo/sgo"
)

func main() {
	if len(os.Args) == 1 {
		errs := sgo.TranslateFile(func() (io.Writer, error) { return os.Stdout, nil }, os.Stdin, "stdin.sgo")
		if len(errs) > 0 {
			reportErrs(errs...)
			os.Exit(1)
		}
		return
	}

	var buildFlags []string
	var pathArgs []string
	for i, arg := range os.Args[1:] {
		if arg[0] == '-' {
			buildFlags = append(buildFlags, arg)
		} else {
			pathArgs = os.Args[i+1:]
			break
		}
	}

	warnings, errs := sgo.TranslatePaths(pathArgs)
	reportErrs(warnings...)
	reportErrs(errs...)
	if len(errs) > 0 {
		os.Exit(1)
	}
}

func reportErrs(errs ...error) {
	for _, err := range errs {
		if errs, ok := err.(scanner.ErrorList); ok {
			for _, err := range errs {
				fmt.Fprintln(os.Stderr, err)
			}
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
	}
}
