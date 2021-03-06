// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements various field and method lookup functions.

package types

import (
	"fmt"
	"strings"
)

// LookupFieldOrMethod looks up a field or method with given package and name
// in T and returns the corresponding *Var or *Func, an index sequence, and a
// bool indicating if there were any pointer indirections on the path to the
// field or method. If addressable is set, T is the type of an addressable
// variable (only matters for method lookups).
//
// The last index entry is the field or method index in the (possibly embedded)
// type where the entry was found, either:
//
//	1) the list of declared methods of a named type; or
//	2) the list of all methods (method set) of an interface type; or
//	3) the list of fields of a struct type.
//
// The earlier index entries are the indices of the anonymous struct fields
// traversed to get to the found entry, starting at depth 0.
//
// If no entry is found, a nil object is returned. In this case, the returned
// index and indirect values have the following meaning:
//
//	- If index != nil, the index sequence points to an ambiguous entry
//	(the same name appeared more than once at the same embedding level).
//
//	- If indirect is set, a method with a pointer receiver type was found
//      but there was no pointer on the path from the actual receiver type to
//	the method's formal receiver base type, nor was the receiver addressable.
//
func LookupFieldOrMethod(T Type, addressable bool, pkg *Package, name string) (obj Object, index []int, indirect bool) {
	// Methods cannot be associated to a named pointer type
	// (spec: "The type denoted by T is called the receiver base type;
	// it must not be a pointer or interface type and it must be declared
	// in the same package as the method.").
	// Thus, if we have a named pointer type, proceed with the underlying
	// pointer type but discard the result if it is a method since we would
	// not have found it for T (see also issue 8590).
	//
	// Same applies for optional wrapping pointers.
	if t, _ := T.(*Named); t != nil {
		var t Type = t
		if o, _ := t.Underlying().(*Optional); o != nil {
			t = o.elem
		}
		if p, _ := t.Underlying().(*Pointer); p != nil {
			obj, index, indirect = lookupFieldOrMethod(p, false, pkg, name)
			if _, ok := obj.(*Func); ok {
				return nil, nil, false
			}
			return
		}
	}

	return lookupFieldOrMethod(T, addressable, pkg, name)
}

// TODO(gri) The named type consolidation and seen maps below must be
//           indexed by unique keys for a given type. Verify that named
//           types always have only one representation (even when imported
//           indirectly via different packages.)

func lookupFieldOrMethod(T Type, addressable bool, pkg *Package, name string) (obj Object, index []int, indirect bool) {
	// WARNING: The code in this function is extremely subtle - do not modify casually!
	//          This function and NewMethodSet should be kept in sync.

	if name == "_" {
		return // blank fields/methods are never found
	}

	typ, isOpt := deopt(T)
	typ, isPtr := deref(typ)

	// *typ where typ is an interface has no methods.
	if isPtr && IsInterface(typ) {
		return
	}

	// Start with typ as single entry at shallowest depth.
	current := []embeddedType{{typ, nil, isPtr, isOpt, false}}

	// Named types that we have seen already, allocated lazily.
	// Used to avoid endless searches in case of recursive types.
	// Since only Named types can be used for recursive types, we
	// only need to track those.
	// (If we ever allow type aliases to construct recursive types,
	// we must use type identity rather than pointer equality for
	// the map key comparison, as we do in consolidateMultiples.)
	var seen map[*Named]bool

	// search current depth
	for len(current) > 0 {
		var next []embeddedType // embedded types found at current depth

		// look for (pkg, name) in all types at current depth
		for _, e := range current {
			typ := e.typ

			// If we have a named type, we may have associated methods.
			// Look for those first.
			if named, _ := typ.(*Named); named != nil {
				if seen[named] {
					// We have seen this type before, at a more shallow depth
					// (note that multiples of this type at the current depth
					// were consolidated before). The type at that depth shadows
					// this same type at the current depth, so we can ignore
					// this one.
					continue
				}
				if seen == nil {
					seen = make(map[*Named]bool)
				}
				seen[named] = true

				// look for a matching attached method
				if i, m := lookupMethod(named.methods, pkg, name); m != nil {
					// potential match
					assert(m.typ != nil)
					index = concat(e.index, i)
					if obj != nil || e.multiples {
						return nil, index, false // collision
					}
					if e.opt && !isOptional(m.typ.(*Signature).recv.typ) {
						continue
					}
					obj = m
					indirect = e.indirect
					continue // we can't have a matching field or interface method
				}

				// continue with underlying type
				typ = named.underlying
			}

			switch t := typ.(type) {
			case *Struct:
				// look for a matching field and collect embedded types
				for i, f := range t.fields {
					if !isOpt && f.sameId(pkg, name) {
						assert(f.typ != nil)
						index = concat(e.index, i)
						if obj != nil || e.multiples {
							return nil, index, false // collision
						}
						obj = f
						indirect = e.indirect
						continue // we can't have a matching interface method
					}
					// Collect embedded struct fields for searching the next
					// lower depth, but only if we have not seen a match yet
					// (if we have a match it is either the desired field or
					// we have a name collision on the same depth; in either
					// case we don't need to look further).
					// Embedded fields are always of the form T or *T where
					// T is a type name. If e.typ appeared multiple times at
					// this depth, f.typ appears multiple times at the next
					// depth.
					if obj == nil && f.anonymous {
						typ, isOpt := deopt(f.typ)
						typ, isPtr := deref(f.typ)
						// TODO(gri) optimization: ignore types that can't
						// have fields or methods (only Named, Struct, and
						// Interface types need to be considered).
						next = append(next, embeddedType{typ, concat(e.index, i), e.indirect || isPtr, isOpt, e.multiples})
					}
				}

			case *Interface:
				// look for a matching method
				// TODO(gri) t.allMethods is sorted - use binary search
				if i, m := lookupMethod(t.allMethods, pkg, name); m != nil {
					assert(m.typ != nil)
					index = concat(e.index, i)
					if obj != nil || e.multiples {
						return nil, index, false // collision
					}
					obj = m
					indirect = e.indirect
				}
			}
		}

		if obj != nil {
			// found a potential match
			// spec: "A method call x.m() is valid if the method set of (the type of) x
			//        contains m and the argument list can be assigned to the parameter
			//        list of m. If x is addressable and &x's method set contains m, x.m()
			//        is shorthand for (&x).m()".
			if f, _ := obj.(*Func); f != nil {
				var isPtrRecv bool
				if optRecv(f) {
					unwrapped, _ := deopt(f.typ.(*Signature).recv.typ)
					_, isPtrRecv = unwrapped.(*Pointer)
				} else if isOpt {
					return nil, nil, true // optional receiver required
				} else {
					isPtrRecv = ptrRecv(f)
				}
				if isPtrRecv && !indirect && !addressable {
					return nil, nil, true // pointer/addressable receiver required
				}
			}
			return
		}

		current = consolidateMultiples(next)
	}

	return nil, nil, false // not found
}

// embeddedType represents an embedded type
type embeddedType struct {
	typ       Type
	index     []int // embedded field indices, starting with index at depth 0
	indirect  bool  // if set, there was a pointer indirection on the path to this field
	opt       bool  // if set, typ is wrapped in an optional
	multiples bool  // if set, typ appears multiple times at this depth
}

// consolidateMultiples collects multiple list entries with the same type
// into a single entry marked as containing multiples. The result is the
// consolidated list.
func consolidateMultiples(list []embeddedType) []embeddedType {
	if len(list) <= 1 {
		return list // at most one entry - nothing to do
	}

	n := 0                     // number of entries w/ unique type
	prev := make(map[Type]int) // index at which type was previously seen
	for _, e := range list {
		if i, found := lookupType(prev, e.typ); found {
			list[i].multiples = true
			// ignore this entry
		} else {
			prev[e.typ] = n
			list[n] = e
			n++
		}
	}
	return list[:n]
}

func lookupType(m map[Type]int, typ Type) (int, bool) {
	// fast path: maybe the types are equal
	if i, found := m[typ]; found {
		return i, true
	}

	for t, i := range m {
		if Identical(t, typ) {
			return i, true
		}
	}

	return 0, false
}

// MissingMethod returns (nil, false) if V implements T, otherwise it
// returns a missing method required by T and whether it is missing or
// just has the wrong type.
//
// For non-interface types V, or if static is set, V implements T if all
// methods of T are present in V. Otherwise (V is an interface and static
// is not set), MissingMethod only checks that methods of T which are also
// present in V have matching types (e.g., for a type assertion x.(T) where
// x is of interface type V).
//
func MissingMethod(V Type, T *Interface, static bool) (method *Func, wrongType bool) {
	// fast path for common case
	if T.Empty() {
		return
	}

	// TODO(gri) Consider using method sets here. Might be more efficient.

	if ityp, _ := V.Underlying().(*Interface); ityp != nil {
		// TODO(gri) allMethods is sorted - can do this more efficiently
		for _, m := range T.allMethods {
			_, obj := lookupMethod(ityp.allMethods, m.pkg, m.name)
			switch {
			case obj == nil:
				if static {
					return m, false
				}
			case !Identical(obj.Type(), m.typ):
				return m, true
			}
		}
		return
	}

	// A concrete type implements T if it implements all methods of T.
	for _, m := range T.allMethods {
		obj, _, _ := lookupFieldOrMethod(V, false, m.pkg, m.name)

		f, _ := obj.(*Func)
		if f == nil {
			return m, false
		}

		if !Identical(f.typ, m.typ) {
			return m, true
		}
	}

	return
}

// assertableTo reports whether a value of type V can be asserted to have type T.
// It returns (nil, false) as affirmative answer. Otherwise it returns a missing
// method required by V and whether it is missing or just has the wrong type.
func assertableTo(V *Interface, T Type) (method *Func, wrongType bool, needsOptional []OptionablePath) {
	_, needsOptional = FindOptionables(T)
	if len(needsOptional) > 0 {
		return
	}
	// no static check is required if T is an interface
	// spec: "If T is an interface type, x.(T) asserts that the
	//        dynamic type of x implements the interface T."
	if _, ok := T.Underlying().(*Interface); ok && !strict {
		return
	}
	method, wrongType = MissingMethod(T, V, false)
	return
}

// A OptionablePath is a series of steps to reach an optionable type within
// a composite type.
type OptionablePath []OptionablePathStep

func (p OptionablePath) String() string {
	var s []string
	for _, st := range p {
		s = append(s, st.String())
	}
	return strings.Join(s, "'s ")
}

// A OptionablePathStep is a step in a path to an optionable type within a
// composite type.
type OptionablePathStep struct {
	// The Type on which to perform the step. If it's a pointer, slice, array,
	// or channel, the step is taking its element type. If it's a map, and Key
	// is false, the step is taking its value type, and its key type otherwise.
	// For struct, interface and function types, see Field and Param.
	Type Type
	// Whether the step operation is selecting the key of a map type.
	Key bool
	// Field index from a Struct, or, if Type is an Interface, method index
	// whose Param's type is to be taken.
	Field int
	// For interface methods and functions, the parameter whose type to be
	// taken. If lesser than zero, it refers to the signature's abs(Param)-1
	// return type.
	Param int
}

func (st OptionablePathStep) String() string {
	switch typ := st.Type.(type) {
	case *Pointer:
		return "pointee"
	case *Map:
		if st.Key {
			return "key"
		}
		return "value"
	case *Struct:
		return "field " + typ.Field(st.Field).Name()
	case *Interface:
		argOrRet := "argument"
		i := st.Param + 1
		if st.Param < 0 {
			argOrRet = "return type"
			i = -st.Param
		}
		return fmt.Sprintf("method %s's #%d %s", typ.Method(st.Field).Name(), i, argOrRet)
	case *Signature:
		argOrRet := "argument"
		i := st.Param + 1
		if st.Param < 0 {
			argOrRet = "return type"
			i = -st.Param
		}
		return fmt.Sprintf("#%d %s", i, argOrRet)
	default:
		return "element"
	}
}

// FindOptionables returns the optionable types within T, including T itself,
// categorized by whether they can be checked at runtime to be non-optional
// (T itself, *T, and fields of T if T is a struct, transitively) or not
// (channel, slice and array element types, map keys and value types, function
// and interface method argument and return types).
func FindOptionables(T Type) (checkable, uncheckable []OptionablePath) {
	findOptionables2(T, nil, false, false, &checkable, &uncheckable)
	return
}

func findOptionables2(T Type, path []OptionablePathStep, allUncheckable bool, wrapped bool, checkable, uncheckable *[]OptionablePath) {
	if allUncheckable {
		checkable = uncheckable
	}
	switch T := T.(type) {
	case *Pointer:
		if !wrapped {
			*checkable = append(*checkable, path)
		}
		findOptionables2(T.Elem(), append(path, OptionablePathStep{Type: T}), allUncheckable, false, checkable, uncheckable)
	case *Map:
		if !wrapped {
			*checkable = append(*checkable, path)
		}
		findOptionables2(T.Key(), append(path, OptionablePathStep{Key: true, Type: T}), true, false, checkable, uncheckable)
		findOptionables2(T.Elem(), append(path, OptionablePathStep{Type: T}), true, false, checkable, uncheckable)
	case *Signature:
		if !wrapped {
			*checkable = append(*checkable, path)
		}
		for i := 0; i < T.Params().Len(); i++ {
			findOptionables2(T.Params().At(i).Type(), append(path, OptionablePathStep{Param: i, Type: T}), true, false, checkable, uncheckable)
		}
		for i := 0; i < T.Results().Len(); i++ {
			findOptionables2(T.Results().At(i).Type(), append(path, OptionablePathStep{Param: -1 - i, Type: T}), true, false, checkable, uncheckable)
		}
	case *Interface:
		if !wrapped {
			*checkable = append(*checkable, path)
		}
		for mi := 0; mi < T.NumMethods(); mi++ {
			f := T.Method(mi).Type().(*Signature)
			for i := 0; i < f.Params().Len(); i++ {
				findOptionables2(f.Params().At(i).Type(), append(path, OptionablePathStep{Field: mi, Param: i, Type: T}), true, false, checkable, uncheckable)
			}
			for i := 0; i < f.Results().Len(); i++ {
				findOptionables2(f.Results().At(i).Type(), append(path, OptionablePathStep{Field: mi, Param: -1 - i, Type: T}), true, false, checkable, uncheckable)
			}
		}
	case *Slice:
		findOptionables2(T.Elem(), append(path, OptionablePathStep{Type: T}), true, false, checkable, uncheckable)
	case *Array:
		findOptionables2(T.Elem(), append(path, OptionablePathStep{Type: T}), true, false, checkable, uncheckable)
	case *Chan:
		if !wrapped {
			*checkable = append(*checkable, path)
		}
		findOptionables2(T.Elem(), append(path, OptionablePathStep{Type: T}), true, false, checkable, uncheckable)
	case *Struct:
		for i, f := range T.fields {
			findOptionables2(f.Type(), append(path, OptionablePathStep{Field: i, Type: T}), allUncheckable, false, checkable, uncheckable)
		}
	case *Named:
		if !wrapped && IsOptionable(T.Underlying()) {
			*checkable = append(*checkable, path)
		}
	case *Optional:
		findOptionables2(T.Elem(), path, allUncheckable, true, checkable, uncheckable)
	}

	return
}

// deref dereferences typ if it is a *Pointer and returns its base and true.
// Otherwise it returns (typ, false).
func deref(typ Type) (Type, bool) {
	if p, _ := typ.(*Pointer); p != nil {
		return p.base, true
	}
	return typ, false
}

// deopt dereferences typ if it is a *Optioanl and returns its elem and true.
// Otherwise it returns (typ, false).
func deopt(typ Type) (Type, bool) {
	if p, _ := typ.(*Optional); p != nil {
		return p.elem, true
	}
	return typ, false
}

// derefStructPtr dereferences typ if it is a (named or unnamed) pointer to a
// (named or unnamed) struct and returns its base. Otherwise it returns typ.
func derefStructPtr(typ Type) Type {
	if p, _ := typ.Underlying().(*Pointer); p != nil {
		if _, ok := p.base.Underlying().(*Struct); ok {
			return p.base
		}
	}
	return typ
}

// concat returns the result of concatenating list and i.
// The result does not share its underlying array with list.
func concat(list []int, i int) []int {
	var t []int
	t = append(t, list...)
	return append(t, i)
}

// fieldIndex returns the index for the field with matching package and name, or a value < 0.
func fieldIndex(fields []*Var, pkg *Package, name string) int {
	if name != "_" {
		for i, f := range fields {
			if f.sameId(pkg, name) {
				return i
			}
		}
	}
	return -1
}

// lookupMethod returns the index of and method with matching package and name, or (-1, nil).
func lookupMethod(methods []*Func, pkg *Package, name string) (int, *Func) {
	if name != "_" {
		for i, m := range methods {
			if m.sameId(pkg, name) {
				return i, m
			}
		}
	}
	return -1, nil
}
