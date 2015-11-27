package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/tcard/sgo/sgo"
)

func main() {
	f, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	err = sgo.TranslateFile(os.Stdout, bytes.NewReader(f), os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
