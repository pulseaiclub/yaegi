package interp_test

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/pulseaiclub/yaegi/interp"
	"github.com/pulseaiclub/yaegi/stdlib"
)

func TestCloneSharesStdlibIsolatesMain(t *testing.T) {
	base := interp.New(interp.Options{})
	if err := base.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}

	srcA := `package main
import "strings"
func Extension() string { return strings.ToUpper("alpha") }
`
	srcB := `package main
import "strings"
func Extension() string { return strings.ToUpper("beta") }
`

	a := base.Clone(interp.Options{})
	b := base.Clone(interp.Options{})

	if _, err := a.Eval(srcA); err != nil {
		t.Fatalf("eval A: %v", err)
	}
	if _, err := b.Eval(srcB); err != nil {
		t.Fatalf("eval B: %v", err)
	}

	va, err := a.Symbol("Extension")
	if err != nil {
		t.Fatal(err)
	}
	vb, err := b.Symbol("Extension")
	if err != nil {
		t.Fatal(err)
	}

	if got := va.Call(nil)[0].String(); got != "ALPHA" {
		t.Fatalf("A: got %q", got)
	}
	if got := vb.Call(nil)[0].String(); got != "BETA" {
		t.Fatalf("B: got %q", got)
	}

	// Parent template must stay free of main.Extension.
	if _, err := base.Symbol("Extension"); err == nil {
		t.Fatal("expected parent to have no Extension")
	}
}

func TestCloneStdioVirtualization(t *testing.T) {
	base := interp.New(interp.Options{})
	if err := base.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	c := base.Clone(interp.Options{Stdout: &buf})
	if _, err := c.Eval(`package main
import "fmt"
func Extension() { fmt.Print("hello-clone") }
`); err != nil {
		t.Fatal(err)
	}
	v, err := c.Symbol("Extension")
	if err != nil {
		t.Fatal(err)
	}
	v.Call(nil)
	if got := buf.String(); got != "hello-clone" {
		t.Fatalf("stdout=%q", got)
	}
}

func TestClonePluginStyle(t *testing.T) {
	type API struct{ Name string }

	base := interp.New(interp.Options{GoPath: ""})
	if err := base.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}

	ext := interp.Exports{
		"github.com/pulseaiclub/phi/ext/ext": {
			"API": reflect.ValueOf((*API)(nil)),
		},
	}

	load := func(id, body string) string {
		c := base.Clone(interp.Options{GoPath: ""})
		if err := c.Use(ext); err != nil {
			t.Fatal(err)
		}
		src := fmt.Sprintf(`package main
import "github.com/pulseaiclub/phi/ext"
func Extension(api *ext.API) { api.Name = %q }
`, body)
		if _, err := c.Eval(src); err != nil {
			t.Fatal(err)
		}
		v, err := c.Symbol("Extension")
		if err != nil {
			t.Fatal(err)
		}
		api := &API{}
		v.Call([]reflect.Value{reflect.ValueOf(api)})
		return api.Name
	}

	if got := load("a", "one"); got != "one" {
		t.Fatalf("got %q", got)
	}
	if got := load("b", "two"); got != "two" {
		t.Fatalf("got %q", got)
	}
}

func TestCloneFasterThanNewUse(t *testing.T) {
	base := interp.New(interp.Options{})
	if err := base.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}

	const n = 20

	start := time.Now()
	for i := 0; i < n; i++ {
		in := interp.New(interp.Options{})
		if err := in.Use(stdlib.Symbols); err != nil {
			t.Fatal(err)
		}
	}
	newCost := time.Since(start)

	start = time.Now()
	for i := 0; i < n; i++ {
		_ = base.Clone(interp.Options{})
	}
	cloneCost := time.Since(start)

	t.Logf("New+Use x%d = %v (avg %v); Clone x%d = %v (avg %v)",
		n, newCost, newCost/n, n, cloneCost, cloneCost/n)

	if cloneCost*3 > newCost {
		// Clone should be clearly cheaper; allow wide margin for CI noise.
		t.Fatalf("Clone not fast enough: clone=%v new+use=%v", cloneCost, newCost)
	}
}

func TestCloneDoesNotShareMain(t *testing.T) {
	base := interp.New(interp.Options{})
	if err := base.Use(stdlib.Symbols); err != nil {
		t.Fatal(err)
	}
	// Pollute a throwaway clone's main; base and later clones must be clean.
	dirty := base.Clone(interp.Options{})
	if _, err := dirty.Eval(`package main
func Extension() string { return "dirty" }
`); err != nil {
		t.Fatal(err)
	}

	clean := base.Clone(interp.Options{})
	if _, err := clean.Symbol("Extension"); err == nil {
		t.Fatal("fresh clone unexpectedly has Extension")
	}
	if _, err := clean.Eval(`package main
func Extension() string { return "clean" }
`); err != nil {
		t.Fatal(err)
	}
	v, err := clean.Symbol("Extension")
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Call(nil)[0].String(); got != "clean" {
		t.Fatalf("got %q", got)
	}
}

func BenchmarkCloneVsNewUse(b *testing.B) {
	base := interp.New(interp.Options{})
	if err := base.Use(stdlib.Symbols); err != nil {
		b.Fatal(err)
	}

	b.Run("New+Use", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			in := interp.New(interp.Options{})
			if err := in.Use(stdlib.Symbols); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Clone", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = base.Clone(interp.Options{})
		}
	})
}

func BenchmarkCloneLoadExtension(b *testing.B) {
	base := interp.New(interp.Options{})
	if err := base.Use(stdlib.Symbols); err != nil {
		b.Fatal(err)
	}
	src := `package main
import "strings"
func Extension() string { return strings.ToUpper("ok") }
`

	b.Run("New+Use+Eval", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			in := interp.New(interp.Options{})
			if err := in.Use(stdlib.Symbols); err != nil {
				b.Fatal(err)
			}
			if _, err := in.Eval(src); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Clone+Eval", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			in := base.Clone(interp.Options{})
			if _, err := in.Eval(src); err != nil {
				b.Fatal(err)
			}
		}
	})
}
