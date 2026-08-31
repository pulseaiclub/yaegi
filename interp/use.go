package interp

import (
	"errors"
	"flag"
	"fmt"
	"go/constant"
	"log"
	"math/bits"
	"os"
	"path"
	"reflect"
	"strings"

	gen "github.com/pulseaiclub/yaegi/stdlib/generic"
)

// ErrSymbolNotFound is returned by Symbol when the requested name is not found.
var ErrSymbolNotFound = errors.New("symbol not found")

// Symbol returns the reflect.Value of an exported symbol previously defined by
// Eval / EvalPath / Compile+Execute (or registered via Use for binary packages).
//
// The name may be:
//   - a bare identifier, looked up in package "main" (e.g. "Extension")
//   - a package-qualified name "pkg.Name" where pkg is a package name or import
//     path (e.g. "main.Extension", "foo.Bar", "encoding/json.Marshal")
//
// Unlike Eval("pkg.Name"), Symbol does not re-parse or compile source; it reads
// the interpreter's symbol table directly. Prefer Symbol when hosting plugins
// or extensions that need to retrieve an entry point after evaluating a file.
func (interp *Interpreter) Symbol(name string) (reflect.Value, error) {
	pkgPath, symName := splitSymbolName(name)
	if symName == "" || !canExport(symName) {
		return reflect.Value{}, fmt.Errorf("%w: %q", ErrSymbolNotFound, name)
	}

	interp.mutex.RLock()
	defer interp.mutex.RUnlock()

	if v, ok := interp.lookupSrcSymbol(pkgPath, symName); ok {
		return v, nil
	}
	if v, ok := interp.lookupBinSymbol(pkgPath, symName); ok {
		return v, nil
	}
	return reflect.Value{}, fmt.Errorf("%w: %q", ErrSymbolNotFound, name)
}

// splitSymbolName splits "pkg.Name" or "import/path.Name". A bare name is
// treated as belonging to package "main".
func splitSymbolName(name string) (pkg, sym string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ""
	}
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return mainID, name
	}
	return name[:i], name[i+1:]
}

func (interp *Interpreter) lookupSrcSymbol(pkgPath, symName string) (reflect.Value, bool) {
	for _, key := range interp.srcPkgKeys(pkgPath) {
		syms, ok := interp.srcPkg[key]
		if !ok {
			continue
		}
		s, ok := syms[symName]
		if !ok || !canExport(symName) {
			continue
		}
		if v, ok := interp.srcSymbolValue(s); ok {
			return v, true
		}
	}
	return reflect.Value{}, false
}

func (interp *Interpreter) lookupBinSymbol(pkgPath, symName string) (reflect.Value, bool) {
	for _, key := range interp.binPkgKeys(pkgPath) {
		syms, ok := interp.binPkg[key]
		if !ok {
			continue
		}
		v, ok := syms[symName]
		if ok && v.IsValid() {
			return v, true
		}
	}
	return reflect.Value{}, false
}

// srcPkgKeys returns candidate srcPkg keys for a package name or import path.
func (interp *Interpreter) srcPkgKeys(pkgPath string) []string {
	if _, ok := interp.srcPkg[pkgPath]; ok {
		return []string{pkgPath}
	}
	var keys []string
	for path, name := range interp.pkgNames {
		if name == pkgPath {
			if _, ok := interp.srcPkg[path]; ok {
				keys = append(keys, path)
			}
		}
	}
	return keys
}

// binPkgKeys returns candidate binPkg keys for a package name or import path.
func (interp *Interpreter) binPkgKeys(pkgPath string) []string {
	if _, ok := interp.binPkg[pkgPath]; ok {
		return []string{pkgPath}
	}
	var keys []string
	for path, name := range interp.pkgNames {
		if name == pkgPath {
			if _, ok := interp.binPkg[path]; ok {
				keys = append(keys, path)
			}
		}
	}
	// Also match by last path element when pkgNames has no entry.
	if len(keys) == 0 {
		for path := range interp.binPkg {
			if path == pkgPath || pathBase(path) == pkgPath {
				keys = append(keys, path)
			}
		}
	}
	return keys
}

func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// srcSymbolValue converts an interpreter source symbol to a reflect.Value.
// Incomplete or generic functions that cannot be wrapped are skipped.
func (interp *Interpreter) srcSymbolValue(s *symbol) (reflect.Value, bool) {
	switch s.kind {
	case constSym:
		if s.rval.IsValid() {
			return s.rval, true
		}
	case funcSym:
		if s.node == nil || s.node.typ == nil {
			return reflect.Value{}, false
		}
		ft := s.node.typ.TypeOf()
		if ft == nil || ft.Kind() != reflect.Func {
			// Generic or incomplete function; not callable via reflect yet.
			return reflect.Value{}, false
		}
		return genFunctionWrapper(s.node)(interp.frame), true
	case varSym:
		if s.index >= 0 && s.index < len(interp.frame.data) {
			return interp.frame.data[s.index], true
		}
	case typeSym:
		if s.typ != nil {
			if t := s.typ.TypeOf(); t != nil {
				return reflect.New(t), true
			}
		}
	}
	return reflect.Value{}, false
}

// Symbols returns a map of interpreter exported symbol values for the given
// import path. If the argument is the empty string, all known symbols are
// returned.
func (interp *Interpreter) Symbols(importPath string) Exports {
	m := map[string]map[string]reflect.Value{}
	interp.mutex.RLock()
	defer interp.mutex.RUnlock()

	for k, v := range interp.srcPkg {
		if importPath != "" && k != importPath {
			continue
		}
		syms := map[string]reflect.Value{}
		for n, s := range v {
			if !canExport(n) {
				// Skip private non-exported symbols.
				continue
			}
			if val, ok := interp.srcSymbolValue(s); ok {
				syms[n] = val
			}
		}

		if len(syms) > 0 {
			m[k] = syms
		}

		if importPath != "" {
			return m
		}
	}

	if importPath != "" && len(m) > 0 {
		return m
	}

	for k, v := range interp.binPkg {
		if importPath != "" && k != importPath {
			continue
		}
		m[k] = v
		if importPath != "" {
			return m
		}
	}

	return m
}

// getWrapper returns the wrapper type of the corresponding interface, trying
// first the composed ones, or nil if not found.
func getWrapper(n *node, t reflect.Type) reflect.Type {
	p, ok := n.interp.binPkg[t.PkgPath()]
	if !ok {
		return nil
	}
	w := p["_"+t.Name()]
	lm := n.typ.methods()

	// mapTypes may contain composed interfaces wrappers to test against, from
	// most complex to simplest (guaranteed by construction of mapTypes). Find the
	// first for which the interpreter type has all the methods.
	for _, rt := range n.interp.mapTypes[w] {
		match := true
		for i := 1; i < rt.NumField(); i++ {
			// The interpreter type must have all required wrapper methods.
			if _, ok := lm[rt.Field(i).Name[1:]]; !ok {
				match = false
				break
			}
		}
		if match {
			return rt
		}
	}

	// Otherwise return the direct "non-composed" interface.
	return w.Type().Elem()
}

// Use loads binary runtime symbols in the interpreter context so
// they can be used in interpreted code.
func (interp *Interpreter) Use(values Exports) error {
	for k, v := range values {
		importPath := path.Dir(k)
		packageName := path.Base(k)

		if k == "." && v["MapTypes"].IsValid() {
			// Use mapping for special interface wrappers.
			for kk, vv := range v["MapTypes"].Interface().(map[reflect.Value][]reflect.Type) {
				interp.mapTypes[kk] = vv
			}
			continue
		}

		if importPath == "." {
			return fmt.Errorf("export path %[1]q is missing a package name; did you mean '%[1]s/%[1]s'?", k)
		}

		if importPath == selfPrefix {
			interp.hooks.Parse(v)
			continue
		}

		if interp.binPkg[importPath] == nil {
			interp.binPkg[importPath] = make(map[string]reflect.Value)
			interp.pkgNames[importPath] = packageName
		}

		for s, sym := range v {
			interp.binPkg[importPath][s] = sym
		}
		if k == selfPath {
			interp.binPkg[importPath]["Self"] = reflect.ValueOf(interp)
		}
	}

	// Checks if input values correspond to stdlib packages by looking for one
	// well known stdlib package path.
	if _, ok := values["fmt/fmt"]; ok {
		fixStdlib(interp)

		// Load stdlib generic source.
		for _, s := range gen.Sources {
			if _, err := interp.Compile(s); err != nil {
				return err
			}
		}
	}
	return nil
}

// fixStdlib redefines interpreter stdlib symbols to use the standard input,
// output and errror assigned to the interpreter. The changes are limited to
// the interpreter only.
// Note that it is possible to escape the virtualized stdio by
// read/write directly to file descriptors 0, 1, 2.
func fixStdlib(interp *Interpreter) {
	p := interp.binPkg["fmt"]
	if p == nil {
		return
	}

	stdin, stdout, stderr := interp.stdin, interp.stdout, interp.stderr

	p["Print"] = reflect.ValueOf(func(a ...interface{}) (n int, err error) { return fmt.Fprint(stdout, a...) })
	p["Printf"] = reflect.ValueOf(func(f string, a ...interface{}) (n int, err error) { return fmt.Fprintf(stdout, f, a...) })
	p["Println"] = reflect.ValueOf(func(a ...interface{}) (n int, err error) { return fmt.Fprintln(stdout, a...) })

	p["Scan"] = reflect.ValueOf(func(a ...interface{}) (n int, err error) { return fmt.Fscan(stdin, a...) })
	p["Scanf"] = reflect.ValueOf(func(f string, a ...interface{}) (n int, err error) { return fmt.Fscanf(stdin, f, a...) })
	p["Scanln"] = reflect.ValueOf(func(a ...interface{}) (n int, err error) { return fmt.Fscanln(stdin, a...) })

	// Update mapTypes to virtualized symbols as well.
	interp.mapTypes[p["Print"]] = interp.mapTypes[reflect.ValueOf(fmt.Print)]
	interp.mapTypes[p["Printf"]] = interp.mapTypes[reflect.ValueOf(fmt.Printf)]
	interp.mapTypes[p["Println"]] = interp.mapTypes[reflect.ValueOf(fmt.Println)]
	interp.mapTypes[p["Scan"]] = interp.mapTypes[reflect.ValueOf(fmt.Scan)]
	interp.mapTypes[p["Scanf"]] = interp.mapTypes[reflect.ValueOf(fmt.Scanf)]
	interp.mapTypes[p["Scanln"]] = interp.mapTypes[reflect.ValueOf(fmt.Scanln)]

	if p = interp.binPkg["flag"]; p != nil {
		c := flag.NewFlagSet(os.Args[0], flag.PanicOnError)
		c.SetOutput(stderr)
		p["CommandLine"] = reflect.ValueOf(&c).Elem()
	}

	if p = interp.binPkg["log"]; p != nil {
		l := log.New(stderr, "", log.LstdFlags)
		// Restrict Fatal symbols to panic instead of exit.
		p["Fatal"] = reflect.ValueOf(l.Panic)
		p["Fatalf"] = reflect.ValueOf(l.Panicf)
		p["Fatalln"] = reflect.ValueOf(l.Panicln)

		p["Flags"] = reflect.ValueOf(l.Flags)
		p["Output"] = reflect.ValueOf(l.Output)
		p["Panic"] = reflect.ValueOf(l.Panic)
		p["Panicf"] = reflect.ValueOf(l.Panicf)
		p["Panicln"] = reflect.ValueOf(l.Panicln)
		p["Prefix"] = reflect.ValueOf(l.Prefix)
		p["Print"] = reflect.ValueOf(l.Print)
		p["Printf"] = reflect.ValueOf(l.Printf)
		p["Println"] = reflect.ValueOf(l.Println)
		p["SetFlags"] = reflect.ValueOf(l.SetFlags)
		p["SetOutput"] = reflect.ValueOf(l.SetOutput)
		p["SetPrefix"] = reflect.ValueOf(l.SetPrefix)
		p["Writer"] = reflect.ValueOf(l.Writer)

		// Update mapTypes to virtualized symbols as well.
		interp.mapTypes[p["Print"]] = interp.mapTypes[reflect.ValueOf(log.Print)]
		interp.mapTypes[p["Printf"]] = interp.mapTypes[reflect.ValueOf(log.Printf)]
		interp.mapTypes[p["Println"]] = interp.mapTypes[reflect.ValueOf(log.Println)]
		interp.mapTypes[p["Panic"]] = interp.mapTypes[reflect.ValueOf(log.Panic)]
		interp.mapTypes[p["Panicf"]] = interp.mapTypes[reflect.ValueOf(log.Panicf)]
		interp.mapTypes[p["Panicln"]] = interp.mapTypes[reflect.ValueOf(log.Panicln)]
	}

	if p = interp.binPkg["os"]; p != nil {
		p["Args"] = reflect.ValueOf(&interp.args).Elem()
		if interp.specialStdio {
			// Inherit streams from interpreter even if they do not have a file descriptor.
			p["Stdin"] = reflect.ValueOf(&stdin).Elem()
			p["Stdout"] = reflect.ValueOf(&stdout).Elem()
			p["Stderr"] = reflect.ValueOf(&stderr).Elem()
		} else {
			// Inherits streams from interpreter only if they have a file descriptor and preserve original type.
			if s, ok := stdin.(*os.File); ok {
				p["Stdin"] = reflect.ValueOf(&s).Elem()
			}
			if s, ok := stdout.(*os.File); ok {
				p["Stdout"] = reflect.ValueOf(&s).Elem()
			}
			if s, ok := stderr.(*os.File); ok {
				p["Stderr"] = reflect.ValueOf(&s).Elem()
			}
		}
		if !interp.unrestricted {
			// In restricted mode, scripts can only access to a passed virtualized env, and can not write the real one.
			getenv := func(key string) string { return interp.env[key] }
			p["Clearenv"] = reflect.ValueOf(func() { interp.env = map[string]string{} })
			p["ExpandEnv"] = reflect.ValueOf(func(s string) string { return os.Expand(s, getenv) })
			p["Getenv"] = reflect.ValueOf(getenv)
			p["LookupEnv"] = reflect.ValueOf(func(key string) (s string, ok bool) { s, ok = interp.env[key]; return })
			p["Setenv"] = reflect.ValueOf(func(key, value string) error { interp.env[key] = value; return nil })
			p["Unsetenv"] = reflect.ValueOf(func(key string) error { delete(interp.env, key); return nil })
			p["Environ"] = reflect.ValueOf(func() (a []string) {
				for k, v := range interp.env {
					a = append(a, k+"="+v)
				}
				return
			})
		}
	}

	if p = interp.binPkg["math/bits"]; p != nil {
		// Do not trust extracted value maybe from another arch.
		p["UintSize"] = reflect.ValueOf(constant.MakeInt64(bits.UintSize))
	}
}
