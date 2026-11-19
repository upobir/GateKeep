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
	"sync/atomic"

	"gatekeep/internal/db"
	"gatekeep/internal/plugin"
	"gatekeep/internal/plugin/builtin"
	"gatekeep/internal/vault"
	"gatekeep/internal/web"
)

type Server struct {
	url      string
	http     *http.Server
	listener net.Listener
	store    *db.Store
	vault    *vault.Service
	plugins  *plugin.Registry
	hub      *hub

	baseCtx      context.Context
	stopBase     context.CancelFunc
	shutdownOnce sync.Once
	shutdownErr  error

	sessionsMu sync.Mutex
	sessions   map[string]mcpSession

	approvalsMu sync.Mutex
	approvals   map[int64]chan db.ToolCall

	execMu sync.Mutex
	execs  map[int64]*executionRun
}

type executionRun struct {
	ctx    context.Context
	cancel context.CancelFunc
	byUser atomic.Bool
	reason string
	done   chan struct{}
}

func New(port int, store *db.Store) (*Server, error) {
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

	baseCtx, stopBase := context.WithCancel(context.Background())
	srv := &Server{
		url:       "http://" + listener.Addr().String(),
		listener:  listener,
		store:     store,
		hub:       newHub(),
		baseCtx:   baseCtx,
		stopBase:  stopBase,
		sessions:  make(map[string]mcpSession),
		approvals: make(map[int64]chan db.ToolCall),
		execs:     make(map[int64]*executionRun),
	}
	plugins, err := builtin.New()
	if err != nil {
		listener.Close()
		return nil, err
	}
	srv.plugins = plugins
	if store != nil {
		srv.vault = vault.New(store)
		if err := store.ExpirePendingToolCalls(context.Background(), restartExpiryReason); err != nil {
			listener.Close()
			return nil, err
		}
		if err := store.ExpireExecutingToolCalls(context.Background(), restartExecutionReason); err != nil {
			listener.Close()
			return nil, err
		}
		if err := store.ExpireReviewToolCalls(context.Background(), restartReviewReason); err != nil {
			listener.Close()
			return nil, err
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth", srv.handleAuth)
	mux.HandleFunc("POST /api/setup", srv.handleSetup)
	mux.HandleFunc("POST /api/unlock", srv.handleUnlock)
	mux.HandleFunc("POST /api/lock", srv.requireSession(srv.handleLock))
	mux.HandleFunc("PUT /api/settings/unlock-duration", srv.requireSession(srv.handleUnlockDuration))
	mux.HandleFunc("GET /api/settings/approval-timeout", srv.requireSession(srv.handleGetApprovalTimeout))
	mux.HandleFunc("PUT /api/settings/approval-timeout", srv.requireSession(srv.handlePutApprovalTimeout))
	mux.HandleFunc("GET /api/settings/execution-timeout", srv.requireSession(srv.handleGetExecutionTimeout))
	mux.HandleFunc("PUT /api/settings/execution-timeout", srv.requireSession(srv.handlePutExecutionTimeout))
	mux.HandleFunc("GET /api/settings/recent-window", srv.requireSession(srv.handleGetRecentWindow))
	mux.HandleFunc("PUT /api/settings/recent-window", srv.requireSession(srv.handlePutRecentWindow))
	mux.HandleFunc("GET /api/approvals", srv.requireSession(srv.handleListApprovals))
	mux.HandleFunc("GET /api/history", srv.requireSession(srv.handleListHistory))
	mux.HandleFunc("DELETE /api/history", srv.requireSession(srv.handleClearHistory))
	mux.HandleFunc("GET /api/approvals/{id}", srv.requireSession(srv.handleGetApproval))
	mux.HandleFunc("DELETE /api/approvals/{id}", srv.requireSession(srv.handleDeleteApproval))
	mux.HandleFunc("POST /api/approvals/{id}/approve", srv.requireSession(srv.handleApprove))
	mux.HandleFunc("POST /api/approvals/{id}/results", srv.requireSession(srv.handleSeeResults))
	mux.HandleFunc("POST /api/approvals/{id}/send", srv.requireSession(srv.handleSendResults))
	mux.HandleFunc("POST /api/approvals/{id}/reject", srv.requireSession(srv.handleReject))
	mux.HandleFunc("POST /api/approvals/{id}/cancel", srv.requireSession(srv.handleCancel))
	mux.HandleFunc("GET /api/health", srv.requireSession(srv.handleHealth))
	mux.HandleFunc("GET /api/events", srv.requireSession(srv.handleEvents))
	mux.HandleFunc("GET /api/resource-types", srv.requireSession(srv.handleResourceTypes))
	mux.HandleFunc("GET /api/resources", srv.requireSession(srv.handleListResources))
	mux.HandleFunc("POST /api/resources", srv.requireSession(srv.handleCreateResource))
	mux.HandleFunc("POST /api/resources/test", srv.requireSession(srv.handleTestResource))
	mux.HandleFunc("GET /api/resources/{id}", srv.requireSession(srv.handleGetResource))
	mux.HandleFunc("PUT /api/resources/{id}", srv.requireSession(srv.handleUpdateResource))
	mux.HandleFunc("DELETE /api/resources/{id}", srv.requireSession(srv.handleDeleteResource))
	mux.HandleFunc("/mcp", srv.handleMCP)
	mux.Handle("/", spaHandler(content))
	srv.http = &http.Server{
		Handler: mux,
		BaseContext: func(net.Listener) context.Context {
			return baseCtx
		},
	}

	return srv, nil
}

func (s *Server) URL() string {
	return s.url
}

func (s *Server) Serve() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watchHealth(ctx)
	go s.watchSession(ctx)
	go s.watchApprovals(ctx)

	err := s.http.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		// Cancelling the base context ends long-lived streams, such as
		// /api/events. Shutdown alone waits for those handlers to return.
		s.stopBase()
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
