package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"

	"gatekeep/internal/web"
)

type Server struct {
	url      string
	http     *http.Server
	listener net.Listener

	shutdownOnce sync.Once
	shutdownErr  error
}

func New(port int) (*Server, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d", port)
	}

	content, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok || tcpAddr.IP == nil || !tcpAddr.IP.IsLoopback() {
		listener.Close()
		return nil, fmt.Errorf("refusing to bind %s", listener.Addr())
	}

	mux := http.NewServeMux()
	mux.Handle("/", spaHandler(content))

	return &Server{
		url:      "http://" + listener.Addr().String(),
		listener: listener,
		http:     &http.Server{Handler: mux},
	}, nil
}

func (s *Server) URL() string {
	return s.url
}

func (s *Server) Serve() error {
	err := s.http.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		s.shutdownErr = s.http.Shutdown(ctx)
	})
	return s.shutdownErr
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
