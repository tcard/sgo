package importer

type Annotation struct {
	Name     string
	Type     string
	Children []*Annotation
}

func (a *Annotation) Lookup(name string) *Annotation {
	if a == nil {
		return nil
	}
	for _, c := range a.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}
