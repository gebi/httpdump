package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bruston/handlers/gzipped"
)

const (
	errWantInteger           = "n must be an integer"
	errStreamingNotSupported = "your client does not support streaming"
	maxBytes                 = 102400
	maxLines                 = 100
	loopback                 = "127.0.0.1"
)

var (
	jsonPrettyPrint bool
	debugOut        bool
)

func defaultHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if debugOut {
			log.Printf("%s %s", r.Method, r.RequestURI)
		}
		if o := r.Header.Get("Origin"); o != "" {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
			w.Header().Set("Access-Control-Allow-Headers",
				"Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
			if r.Method == "OPTIONS" {
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "The host and port to listen on.")
	flag.BoolVar(&jsonPrettyPrint, "pretty", false, "Pretty print json output")
	flag.BoolVar(&debugOut, "debug", false, "Log requests to stdout")
	flag.Parse()
	http.HandleFunc("/", index)
	http.HandleFunc("/headers", headers)
	http.HandleFunc("/status/", status)
	http.HandleFunc("/ip", ip)
	http.HandleFunc("/get", get)
	http.Handle("/gzip", gzipped.New(http.HandlerFunc(gzip)))
	http.HandleFunc("/user-agent", userAgent)
	http.HandleFunc("/bytes/", writeBytes)
	http.HandleFunc("/stream/", stream)
	http.HandleFunc("/redirect-to", redirectTo)
	http.Handle("/basic-auth/", basicAuth(false))
	http.Handle("/hidden-basic-auth/", basicAuth(true))
	http.HandleFunc("/delay/", delay)
	log.Fatal(http.ListenAndServe(*listen, defaultHandler(http.DefaultServeMux)))
}

func jsonHeader(w http.ResponseWriter) {
	w.Header().Set("Content-type", "application/json")
}

func writeJSON(w http.ResponseWriter, data interface{}, code int) error {
	jsonHeader(w)
	w.WriteHeader(code)
	if !jsonPrettyPrint {
		return json.NewEncoder(w).Encode(data)
	}
	out, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

func index(w http.ResponseWriter, r *http.Request) {
	const index = `<html>
<body id='manpage'>
<h1>httpdump(1): HTTP Request & Response Service</h1>

<h2 id="ENDPOINTS">ENDPOINTS</h2>

<ul>
<li><a href="/" data-bare-link="true"><code>/</code></a> This page.</li>
<li><a href="./ip" data-bare-link="true"><code>/ip</code></a> Returns Origin IP.</li>
<li><a href="./user-agent" data-bare-link="true"><code>/user-agent</code></a> Returns user-agent.</li>
<li><a href="./headers" data-bare-link="true"><code>/headers</code></a> Returns header dict.</li>
<li><a href="./get" data-bare-link="true"><code>/get</code></a> Returns GET data.</li>
<li><a href="./gzip" data-bare-link="true"><code>/gzip</code></a> Returns gzip-encoded data.</li>
<li><a href="./status/418"><code>/status/:code</code></a> Returns given HTTP Status code.</li>
<li><a href="./stream/20"><code>/stream/:n</code></a> Streams <em>n</em>–100 lines.</li>
<li><a href="./bytes/1024"><code>/bytes/:n</code></a> Generates <em>n</em> random bytes of binary data, accepts optional <em>seed</em> integer parameter.</li>
<li><a href="./redirect-to?url=http://example.com/"><code>/redirect-to?url=foo</code></a> 302 Redirects to the <em>foo</em> URL.</li>
<li><a href="./basic-auth/user/passwd"><code>/basic-auth/:user/:passwd</code></a> Challenges HTTPBasic Auth.</li>
<li><a href="./hidden-basic-auth/user/passwd"><code>/hidden-basic-auth/:user/:passwd</code></a> 404'd BasicAuth.</li>
<li><a href="./delay/3"><code>/delay/:n</code></a> Delays responding for <em>n</em>–10 seconds.</li>
</ul>
</body>
</html>
`
	w.Write([]byte(index))
}

func headers(w http.ResponseWriter, r *http.Request) {
	r.Header.Add("Host", r.Host)
	writeJSON(w, r.Header, http.StatusOK)
}

func status(w http.ResponseWriter, r *http.Request) {
	code, err := strconv.Atoi(path.Base(r.URL.Path))
	if err != nil {
		http.Error(w, "status code must be an integer", http.StatusBadRequest)
		return
	}
	w.WriteHeader(code)
}

func getOrigin(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" && forwarded != host {
		if host == loopback {
			return forwarded
		}
		host = fmt.Sprintf("%s, %s", forwarded, host)
	}
	return host
}

func ip(w http.ResponseWriter, r *http.Request) {
	var o struct {
		Origin string `json:"origin"`
	}
	o.Origin = getOrigin(r)
	writeJSON(w, o, http.StatusOK)
}

type request struct {
	Args    url.Values  `json:"args"`
	Gzipped bool        `json:"gzipped,omitempty"`
	Headers http.Header `json:"headers"`
	Origin  string      `json:"origin"`
	URL     string      `json:"url"`
}

func rawURL(r *http.Request) string {
	var scheme string
	if r.TLS == nil {
		scheme = "http"
	} else {
		scheme = "https"
	}
	return scheme + "://" + r.Host + r.URL.String()
}

func getReq(r *http.Request) request {
	ret := request{
		Args:    r.URL.Query(),
		Headers: r.Header,
		Origin:  getOrigin(r),
		URL:     rawURL(r),
	}
	ret.Headers.Add("Host", r.Host)
	return ret
}

func get(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, getReq(r), http.StatusOK)
}

func gzip(w http.ResponseWriter, r *http.Request) {
	req := getReq(r)
	if _, ok := w.(gzipped.GzipResponseWriter); ok {
		req.Gzipped = true
	}
	writeJSON(w, req, http.StatusOK)
}

func userAgent(w http.ResponseWriter, r *http.Request) {
	var resp struct {
		UserAgent string `json:"user-agent"`
	}
	resp.UserAgent = r.Header.Get("User-Agent")
	writeJSON(w, resp, http.StatusOK)
}

func writeBytes(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(path.Base(r.URL.Path))
	if err != nil || n < 0 || n > maxBytes {
		http.Error(w, fmt.Sprintf("number of bytes must be in range: 0 - %d", maxBytes), http.StatusBadRequest)
		return
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

func min(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

func stream(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(path.Base(r.URL.Path))
	if err != nil || n < 0 {
		http.Error(w, errWantInteger, http.StatusBadRequest)
		return
	}
	n = min(n, maxLines)
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, errStreamingNotSupported, http.StatusBadRequest)
		return
	}
	req := getReq(r)
	jsonHeader(w)
	for i := 0; i < n; i++ {
		if err := json.NewEncoder(w).Encode(req); err != nil {
			return
		}
		f.Flush()
	}
}

func redirectTo(w http.ResponseWriter, r *http.Request) {
	dst := r.URL.Query().Get("url")
	if _, err := url.Parse(dst); dst == "" || err != nil {
		http.Error(w, "bad URL", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, dst, http.StatusFound)
}

type authedResponse struct {
	Authenticated bool   `json:"authenticated"`
	User          string `json:"user"`
}

func basicAuth(hidden bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params := strings.Split(r.URL.Path, "/")
		if len(params) < 4 {
			http.NotFound(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != params[2] || p != params[3] {
			if !hidden {
				w.Header().Set("WWW-Authenticate", "Basic realm=\"httpdump\"")
				w.WriteHeader(http.StatusUnauthorized)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		writeJSON(w, authedResponse{true, u}, http.StatusOK)
	})
}

func delay(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(path.Base(r.URL.Path))
	if err != nil {
		http.Error(w, "you must specify a delay", http.StatusBadRequest)
		return
	}
	n = min(n, 10)
	if n > 0 {
		<-time.After(time.Second * time.Duration(n))
	}
	writeJSON(w, getReq(r), http.StatusOK)
}
