// Package annotations provides utilities to work with SGo annotation files.
package annotations

import "strings"

// TODO: Translate this file to SGo when we have optional method receivers.

// An Annotation holds SGo type annotations for a Go package or identifier, and
// its children. If its Cursor is empty, it refers to a Go package. From there,
// use Lookup to get annotations to its declared identifiers, and from those to
// their subidentifiers (struct fields, etc.).
type Annotation struct {
	cursor string
	typ    string
	anns   map[string]string
}

// NewAnnotation returns an Annotation for a map from
func NewAnnotation(anns map[string]string) *Annotation {
	if anns == nil {
		return nil
	}
	return &Annotation{anns: anns}
}

// Cursor returns the cursor, or path, from the package's Annotation to the
// receiver Annotation, separated by '.'.
func (a *Annotation) Cursor() string {
	return a.cursor
}

// Type returns the SGo type annotation for package or identifier referred to by
// Cursor, if it exists.
func (a *Annotation) Type() (string, bool) {
	if a == nil || a.typ == "" {
		return "", false
	}
	return a.typ, true
}

// String implements fmt.Stringer for Annotation.
func (a *Annotation) String() string {
	if typ, ok := a.Type(); ok {
		return "type: " + typ
	}
	var ks []string
	for k := range a.anns {
		ks = append(ks, k)
	}
	return a.cursor + " -> [" + strings.Join(ks, ", ") + "]"
}

// Lookup finds a child Annotation of the receiver with the given identifier.
func (a *Annotation) Lookup(name string) *Annotation {
	if a == nil || a.anns == nil {
		return nil
	}
	cursor := name
	if a.cursor != "" {
		cursor = a.cursor + "." + cursor
	}
	v, ok := a.anns[cursor]
	if ok {
		return &Annotation{typ: v}
	}
	return &Annotation{cursor: cursor, anns: a.anns}
}
