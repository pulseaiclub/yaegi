package interp

import (
	"path"
	"reflect"
)

// Clone returns a new interpreter that reuses packages already loaded into
// interp (typically after a single Use(stdlib.Symbols) on a template).
//
// The clone has its own execution frame and package "main", so multiple clones
// can each Eval a separate extension with `package main` without clobbering
// one another, while sharing the cost of loading the standard library.
//
// Shared (read-mostly) state includes binary packages, compiled source
// packages such as generic stdlib, mapTypes, and hooks. stdio-related
// symbols are re-virtualized for the clone's Options (Stdin/Stdout/Stderr).
//
// The parent should be treated as a template after Clone: avoid further Eval
// that mutates shared package scopes. Prefer Clone only for spawning isolated
// evaluation contexts from a fully initialized base.
func (interp *Interpreter) Clone(opts Options) *Interpreter {
	child := New(opts)

	interp.mutex.RLock()
	defer interp.mutex.RUnlock()

	child.binPkg = cloneExports(interp.binPkg)
	if child.binPkg[""] == nil {
		child.binPkg[""] = map[string]reflect.Value{}
	}
	// Keep a distinct _error wrapper type for this interpreter.
	child.binPkg[""]["_error"] = reflect.ValueOf((*_error)(nil))

	child.pkgNames = cloneStringMap(interp.pkgNames)
	child.mapTypes = cloneMapTypes(interp.mapTypes)

	// Reuse compiled source packages (generics, etc.), but never share main.
	child.srcPkg = make(imports, len(interp.srcPkg))
	for k, syms := range interp.srcPkg {
		if k == mainID {
			continue
		}
		child.srcPkg[k] = syms
	}

	child.scopes = make(map[string]*scope, len(interp.scopes))
	for k, sc := range interp.scopes {
		if k == mainID {
			continue
		}
		child.scopes[k] = sc
	}

	child.generic = make(map[string]*node, len(interp.generic))
	for k, n := range interp.generic {
		child.generic[k] = n
	}

	// Snapshot global frame so indices in shared symbols remain valid.
	child.frame.data = make([]reflect.Value, len(interp.frame.data))
	copy(child.frame.data, interp.frame.data)

	// Match the parent's global type/frame layout. New symbols compiled in the
	// clone append beyond this prefix.
	child.universe.types = append([]reflect.Type(nil), interp.universe.types...)

	// Copy package symbols missing from the fresh universe (e.g. "slices").
	for name, sym := range interp.universe.sym {
		if _, ok := child.universe.sym[name]; !ok {
			child.universe.sym[name] = sym
		}
	}

	if interp.hooks != nil && len(interp.hooks.convert) > 0 {
		child.hooks.convert = append([]convertFn(nil), interp.hooks.convert...)
	}

	// Virtualize fmt/log/os against this clone's stdio and env.
	if child.binPkg["fmt"] != nil {
		fixStdlib(child)
	}

	// If the parent exposed the interpreter itself, point Self at the clone.
	if p := child.binPkg[path.Dir(selfPath)]; p != nil {
		if _, ok := p["Self"]; ok {
			p["Self"] = reflect.ValueOf(child)
		}
	}

	return child
}

func cloneExports(in Exports) Exports {
	if in == nil {
		return Exports{}
	}
	out := make(Exports, len(in))
	for path, syms := range in {
		cp := make(map[string]reflect.Value, len(syms))
		for name, v := range syms {
			cp[name] = v
		}
		out[path] = cp
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneMapTypes(in map[reflect.Value][]reflect.Type) map[reflect.Value][]reflect.Type {
	if in == nil {
		return map[reflect.Value][]reflect.Type{}
	}
	out := make(map[reflect.Value][]reflect.Type, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
