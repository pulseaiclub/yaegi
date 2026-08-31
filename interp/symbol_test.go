package interp_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/pulseaiclub/yaegi/interp"
	"github.com/pulseaiclub/yaegi/stdlib"
)

func TestSymbolMainPackage(t *testing.T) {
	i := interp.New(interp.Options{})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}

	src := `package main

func Extension() string { return "ok" }

var Version = "1.0"
`

	if _, err := i.Eval(src); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"Extension", "main.Extension"} {
		v, err := i.Symbol(name)
		if err != nil {
			t.Fatalf("Symbol(%q): %v", name, err)
		}
		if v.Kind() != reflect.Func {
			t.Fatalf("Symbol(%q): got kind %v, want Func", name, v.Kind())
		}
		out := v.Call(nil)
		if got := out[0].String(); got != "ok" {
			t.Fatalf("Symbol(%q)(): got %q, want %q", name, got, "ok")
		}
	}

	v, err := i.Symbol("Version")
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "1.0" {
		t.Fatalf("Version: got %q, want %q", got, "1.0")
	}
}

func TestSymbolQualifiedPackage(t *testing.T) {
	i := interp.New(interp.Options{})

	src := `package foo

func Bar(s string) string { return s + "-Foo" }
`

	if _, err := i.Eval(src); err != nil {
		t.Fatal(err)
	}

	v, err := i.Symbol("foo.Bar")
	if err != nil {
		t.Fatal(err)
	}
	out := v.Call([]reflect.Value{reflect.ValueOf("Kung")})
	if got := out[0].String(); got != "Kung-Foo" {
		t.Fatalf("got %q, want Kung-Foo", got)
	}
}

func TestSymbolNotFound(t *testing.T) {
	i := interp.New(interp.Options{})
	if _, err := i.Eval(`package main; func Foo() {}`); err != nil {
		t.Fatal(err)
	}

	_, err := i.Symbol("Missing")
	if !errors.Is(err, interp.ErrSymbolNotFound) {
		t.Fatalf("got %v, want ErrSymbolNotFound", err)
	}

	_, err = i.Symbol("unexported")
	if !errors.Is(err, interp.ErrSymbolNotFound) {
		t.Fatalf("unexported: got %v, want ErrSymbolNotFound", err)
	}
}

func TestSymbolBinPackage(t *testing.T) {
	i := interp.New(interp.Options{})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}

	v, err := i.Symbol("fmt.Sprintf")
	if err != nil {
		t.Fatal(err)
	}
	out := v.Call([]reflect.Value{
		reflect.ValueOf("n=%d"),
		reflect.ValueOf(42),
	})
	if got := out[0].String(); got != "n=42" {
		t.Fatalf("got %q, want n=42", got)
	}
}

func TestSymbolsNoPanicOnGenerics(t *testing.T) {
	i := interp.New(interp.Options{})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Eval(`package main; func Extension() string { return "ok" }`); err != nil {
		t.Fatal(err)
	}

	// Previously paniced on generic stdlib source packages (slices/maps/cmp).
	syms := i.Symbols("")
	if _, ok := syms["main"]["Extension"]; !ok {
		t.Fatal("expected main.Extension in Symbols(\"\")")
	}
}

func TestSymbolPluginStyle(t *testing.T) {
	// Mirrors Phi extension loading: Eval source, then Symbol("Extension").
	type API struct{ N int }

	i := interp.New(interp.Options{GoPath: ""})
	if err := i.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	if err := i.Use(interp.Exports{
		"example.com/ext/ext": {
			"API": reflect.ValueOf((*API)(nil)),
		},
	}); err != nil {
		t.Fatal(err)
	}

	src := `package main

import "example.com/ext"

func Extension(api *ext.API) {
	api.N = 7
}
`
	if _, err := i.Eval(src); err != nil {
		t.Fatal(err)
	}

	v, err := i.Symbol("Extension")
	if err != nil {
		t.Fatal(err)
	}
	api := &API{}
	v.Call([]reflect.Value{reflect.ValueOf(api)})
	if api.N != 7 {
		t.Fatalf("got N=%d, want 7", api.N)
	}
}
