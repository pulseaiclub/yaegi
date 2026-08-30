package main

import (
	"github.com/pulseaiclub/yaegi/interp"
)

func main() {
	i := interp.New(interp.Opt{})
	i.Eval(`println("Hello")`)
}

// Output:
// Hello
