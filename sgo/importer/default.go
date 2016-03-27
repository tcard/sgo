package importer

var defaultAnnotations = map[string]map[string]string{
	"os": {
		"Stdin":  `*File`,
		"Stdout": `*File`,
		"Stderr": `*File`,
	},
	"os/exec": {
		"Command": `func (name string, arg ...string) *Cmd`,
	},
	"html/template": {
		"New":  `func(name string) *Template`,
		"Must": `func(t ?*Template, err ?error) *Template`,
	},
	"strings": {
		"NewReader": `func(s string) *Reader`,
	},
	"go/scanner": {
		"ErrorList": `[]*Error`,
	},
	"errors": {
		"New": `func(text string) error`,
	},
	"net/http": {
		"PostForm":    `func(url string, data url.Values) (resp *Response \ err error)`,
		"HandleFunc":  `func(pattern string, handler func(ResponseWriter, *Request))`,
		"Request.URL": `*url.URL`,
	},
	"encoding/json": {
		"NewDecoder": `func(r io.Reader) *Decoder`,
	},
	"flag": {
		"String": `func(name string, value string, usage string) *string`,
	},

	// Non-std
	"github.com/gorilla/websocket": {
		"(*Upgrader).Upgrade": `func(w http.ResponseWriter, r *http.Request, responseHeader ?http.Header) (*Conn \ error)`,
	},
}
