package interp_test

import (
	"testing"

	"github.com/pulseaiclub/yaegi/interp"
	"github.com/pulseaiclub/yaegi/stdlib"
)

func setupInterp(b *testing.B) *interp.Interpreter {
	b.Helper()
	i := interp.New(interp.Options{GoPath: ""})
	if err := i.Use(stdlib.Symbols); err != nil {
		b.Fatal(err)
	}
	src := `package main

func Extension() string { return "ok" }

func Other1() {}
func Other2() {}
func Other3() {}
var Version = "1.0"
`
	if _, err := i.Eval(src); err != nil {
		b.Fatal(err)
	}
	return i
}

func BenchmarkSymbolLookup(b *testing.B) {
	i := setupInterp(b)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		v, err := i.Symbol("Extension")
		if err != nil || !v.IsValid() {
			b.Fatal(err)
		}
	}
}

func BenchmarkSymbolLookupQualified(b *testing.B) {
	i := setupInterp(b)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		v, err := i.Symbol("main.Extension")
		if err != nil || !v.IsValid() {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvalLookup(b *testing.B) {
	i := setupInterp(b)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		v, err := i.Eval("Extension")
		if err != nil || !v.IsValid() {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvalLookupQualified(b *testing.B) {
	i := setupInterp(b)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		v, err := i.Eval("main.Extension")
		if err != nil || !v.IsValid() {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvalLookupPhiStyle(b *testing.B) {
	// Mirrors Phi's dual-candidate loop (qualified first, then bare).
	i := setupInterp(b)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		v, err := i.Eval("main.Extension")
		if err != nil || !v.IsValid() {
			v, err = i.Eval("Extension")
		}
		if err != nil || !v.IsValid() {
			b.Fatal(err)
		}
	}
}

func BenchmarkSymbolsMapLookup(b *testing.B) {
	i := setupInterp(b)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		m := i.Symbols("main")
		v, ok := m["main"]["Extension"]
		if !ok || !v.IsValid() {
			b.Fatal("not found")
		}
	}
}
