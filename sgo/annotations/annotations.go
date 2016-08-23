// Package annotations provides utilities to work with SGo annotation files.
package annotations

import "strings"

// TODO: Translate this file to SGo when we have optional method receivers.

type Annotation struct {
	cursor string
	typ    string
	anns   map[string]string
}

func NewAnnotation(anns map[string]string) *Annotation {
	if anns == nil {
		return nil
	}
	return &Annotation{anns: anns}
}

func (a *Annotation) Cursor() string {
	return a.cursor
}

func (a *Annotation) Type() (string, bool) {
	if a == nil || a.typ == "" {
		return "", false
	}
	return a.typ, true
}

func (a *Annotation) String() string {
	if typ, ok := a.Type(); ok {
		return "type: " + typ
	}
	var ks []string
	for k, _ := range a.anns {
		ks = append(ks, k)
	}
	return a.cursor + " -> [" + strings.Join(ks, ", ") + "]"
}

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
