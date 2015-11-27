package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/tcard/sgo/sgo"
)

var (
	httpAddr = flag.String("http", ":5600", "HTTP server address")

	upgrader = websocket.Upgrader{}
)

func main() {
	flag.Parse()

	msgCh := make(chan msgType)
	go func() {
		for msg := range msgCh {
			switch msg.Type {
			case "translate":
				resp := &msgType{
					Type: "translate",
				}
				func() {
					defer func() {
						if r := recover(); r != nil {
							value := fmt.Sprintln(r)
							stack := make([]byte, 1000)
							runtime.Stack(stack, false)
							value += string(stack)
							resp.Value = value
						}
					}()
					w := &bytes.Buffer{}
					err := sgo.TranslateFile(w, strings.NewReader(msg.Value.(string)), "name")
					if err != nil {
						resp.Value = err.Error()
					} else {
						resp.Value = w.String()
					}
				}()
				msg.c.WriteJSON(resp)
			case "execute":
				resp := &msgType{
					Type: "execute",
				}
				body := url.Values{}
				body.Add("version", "2")
				var err error
				w := &bytes.Buffer{}
				func() {
					defer func() {
						if r := recover(); r != nil {
							value := fmt.Sprintln(r)
							stack := make([]byte, 1000)
							runtime.Stack(stack, false)
							value += string(stack)
							err = errors.New(value)
						}
					}()

					err = sgo.TranslateFile(w, strings.NewReader(msg.Value.(string)), "name")
				}()
				if err != nil {
					resp.Value = err.Error()
				} else {
					body.Add("body", w.String())
					postResp, err := http.PostForm("http://play.golang.org/compile", body)
					if err != nil {
						resp.Value = err.Error()
					} else {
						var v interface{}
						err := json.NewDecoder(postResp.Body).Decode(&v)
						postResp.Body.Close()
						if err != nil {
							resp.Value = err.Error()
						} else {
							resp.Value = v
						}
					}
				}
				msg.c.WriteJSON(resp)
			}
		}
	}()

	http.HandleFunc("/ws", func(w http.ResponseWriter, req *http.Request) {
		c, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			log.Println("upgrade:", err)
			return
		}
		defer c.Close()
		for {
			var recvMsg msgType
			err := c.ReadJSON(&recvMsg)
			if err != nil {
				log.Println("read:", err)
				break
			}
			recvMsg.c = c

			msgCh <- recvMsg
		}

	})

	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		indexTpl.Execute(w, "ws://"+req.Host+"/ws")
	})

	fmt.Println("Serving on", *httpAddr)
	log.Fatal(http.ListenAndServe(*httpAddr, nil))
}

type msgType struct {
	Type  string      `json:"type"`
	Value interface{} `json:"value"`
	c     *websocket.Conn
}

var indexTpl = template.Must(template.New("index").Parse(`
<!DOCTYPE html>
<html lang="en">

<head>
  <meta charset="utf-8">
  <title>SGo playground</title>
</head>

<body>

<div style="width: 50%; float: left;">
<textarea id="input-code" style="width: 90%;" rows="30">
package main

type Result struct {
	a int
}

type Err string

// Error returns the error string
func (e Err) Error() string { return string(e) }

func Foo(i int) (*Result, ?error) {
	if i % 2 == 0 {
		return &Result{i}, nil
	}
	// return nil, Err("hola") -- doesn't compile
	// return nil, nil         -- doesn't compile
	return &Result{i}, Err("hola")
}

func main() {
	a, b := Foo(123)
	if b == nil {
		println(b)
	} else {
		println("HEY", b)
	}
	println(a, b)
}
</textarea>
</div>

<div>
<pre id="translated">
</pre>
</div>

<div style="clear: both;">
  <button id="run-button">Run</button>

  <div>
  <pre id="executed">
  </pre>
  </div>

</div>

<script>
window.addEventListener("load", function(evt) {
    var inputCode = document.getElementById("input-code");
    var translated = document.getElementById("translated");
    var runButton = document.getElementById("run-button");
    var executed = document.getElementById("executed");

    var ws = new WebSocket("{{.}}");
    ws.onmessage = function(ev) {
    	var data = JSON.parse(ev.data);
    	if (data.type == "execute") {
    		if (data.value.Events) {
    			var evs = data.value.Events;
    			var accDelay = 0;
    			for (var i in evs) {
    			(function(i) {
    				var ev = evs[i];
    				accDelay += ev.Delay;
    				setTimeout(function() {
    					executed.innerHTML += ev.Message;
    					if (i == evs.length - 1) {
    						runButton.innerHTML = "Run";
    						runButton.disabled = false;
    					}
    				}, accDelay);
    			})(i);
				}
    		} else if (data.value.Errors) {
    			executed.innerHTML = data.value.Errors;
    			runButton.innerHTML = "Run";
    			runButton.disabled = false;
    		} else {
    			runButton.innerHTML = "Run";
    			runButton.disabled = false;
    		}
    	} else if (data.type == "translate") {
    		translated.innerHTML = data.value;
    	}
    };

    runButton.onclick = function(ev) {
    	ev.preventDefault();
        ws.send(JSON.stringify({
        	"type": "execute",
        	"value": inputCode.value,
        }));
		runButton.innerHTML = "Running...";
		runButton.disabled = true;
		executed.innerHTML = "";
    };

    var translate = function() {
        ws.send(JSON.stringify({
        	"type": "translate",
        	"value": inputCode.value,
        }));
    };

    inputCode.onchange = translate;
    inputCode.onkeyup = translate;
    ws.onopen = function() {
    	translate();
    };
});
</script>
</body>

</html>
`))
