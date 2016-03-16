package importer

var defaultAnnotations = map[string]*Annotation{
	"os": {
		Name: "",
		Children: []*Annotation{
			{
				Name: "Stdin",
				Type: "*File",
			},
			{
				Name: "Stdout",
				Type: "*File",
			},
			{
				Name: "Stderr",
				Type: "*File",
			},
		},
	},
	"os/exec": {
		Name: "",
		Children: []*Annotation{
			{
				Name: "Command",
				Type: "func (name string, arg ...string) *Cmd",
			},
		},
	},
}
