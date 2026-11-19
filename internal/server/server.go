package server

import (
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"gatekeep/internal/web"
)

func ListenAndServe(addr string) error {
	content, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("/", spaHandler(content))

	log.Printf("listening on http://%s", addr)
	return http.ListenAndServe(addr, mux)
}

func spaHandler(content fs.FS) http.Handler {
	files := http.FileServer(http.FS(content))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			serveIndex(w, r, content)
			return
		}

		f, err := content.Open(path)
		if err != nil {
			serveIndex(w, r, content)
			return
		}
		stat, statErr := f.Stat()
		f.Close()
		if statErr != nil || stat.IsDir() {
			serveIndex(w, r, content)
			return
		}

		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, content fs.FS) {
	index, err := content.Open("index.html")
	if err != nil {
		http.Error(w, "frontend is not embedded", http.StatusInternalServerError)
		return
	}
	defer index.Close()

	stat, err := index.Stat()
	if err != nil {
		http.Error(w, "frontend is not embedded", http.StatusInternalServerError)
		return
	}

	rs, ok := index.(io.ReadSeeker)
	if !ok {
		http.Error(w, "frontend is not embedded", http.StatusInternalServerError)
		return
	}

	http.ServeContent(w, r, "index.html", stat.ModTime(), rs)
}
