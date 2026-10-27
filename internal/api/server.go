package api

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/samuel-stidham/grocery-list-core/internal/meta"
	"github.com/samuel-stidham/grocery-list-core/internal/store"
)

// Options configure a Server.
type Options struct {
	// Version is reported by /v1/about.
	Version string
	// Logger receives one record per request and the cause of every 5xx.
	// Nil means the slog default.
	Logger *slog.Logger
	// Hosts are Host header values to serve beyond the loopback names. The
	// serve command passes the host part of --listen.
	Hosts []string
}

// Server holds the database, the logger, and the routes.
type Server struct {
	database *sql.DB
	version  string
	logger   *slog.Logger
	// info names and units every nutrient the API always reports, so the
	// minimum keys can be filled in for foods that lack a value.
	info map[int64]store.NutrientInfo
	mux  *http.ServeMux
	// allowed records the methods registered per pattern, for the Allow
	// header on a 405.
	allowed map[string][]string
	// hosts is the set of Host header values served, lowercased, no port.
	hosts map[string]bool
	// about is computed once. The database never changes while served.
	about aboutResponse
}

// loopbackHosts are the Host values a client sends to a loopback server. A
// browser tricked by DNS rebinding sends the attacker's name instead, and is
// refused. The data is public, so this is belt and braces.
func loopbackHosts() []string {
	return []string{"127.0.0.1", "localhost", "::1"}
}

// New builds a server over an open read-only database.
func New(ctx context.Context, database *sql.DB, opts Options) (*Server, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	info, err := store.Nutrients(ctx, database, scoredNutrientIDs())
	if err != nil {
		return nil, fmt.Errorf("load nutrient names: %w", err)
	}

	for _, nutrientID := range scoredNutrientIDs() {
		if _, present := info[nutrientID]; !present {
			return nil, fmt.Errorf("%w: nutrient %d", ErrNutrientMissing, nutrientID)
		}
	}

	about, err := buildAbout(ctx, database, opts.Version)
	if err != nil {
		return nil, err
	}

	server := &Server{
		database: database,
		about:    about,
		version:  opts.Version,
		logger:   logger,
		info:     info,
		mux:      http.NewServeMux(),
		allowed:  map[string][]string{},
		hosts:    map[string]bool{},
	}

	// An empty host comes from -listen :PORT and would accept a request
	// with no Host header at all, so it is skipped.
	for _, host := range append(loopbackHosts(), opts.Hosts...) {
		if name := hostOnly(host); name != "" {
			server.hosts[name] = true
		}
	}

	server.routes()

	return server, nil
}

// hostOnly lowercases a Host value and drops the port and IPv6 brackets.
func hostOnly(hostport string) string {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}

	return strings.Trim(strings.ToLower(host), "[]")
}

func (server *Server) routes() {
	server.route(http.MethodGet, "/v1/about", server.handleAbout)
	server.route(http.MethodGet, "/v1/foods", server.handleSearch)
	server.route(http.MethodGet, "/v1/foods/{fdc_id}", server.handleFood)
	server.route(http.MethodGet, "/v1/foods/{fdc_id}/portions", server.handlePortions)
	server.route(http.MethodPost, "/v1/score", server.handleScore)
	server.mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		writeError(writer, http.StatusNotFound, codeNotFound, "no such route: "+request.URL.Path)
	})
}

// route registers the handler for one method and a JSON 405 for the others.
// The 405 carries the Allow header RFC 9110 requires.
func (server *Server) route(method, pattern string, handler http.HandlerFunc) {
	server.mux.HandleFunc(method+" "+pattern, handler)

	first := len(server.allowed[pattern]) == 0
	server.allowed[pattern] = append(server.allowed[pattern], method)

	if !first {
		return
	}

	server.mux.HandleFunc(pattern, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Allow", strings.Join(server.allowed[pattern], ", "))
		writeError(writer, http.StatusMethodNotAllowed, codeMethod, request.Method+" is not allowed on "+pattern)
	})
}

// Handler returns the root handler. The attribution header goes on every
// response, errors included, before any route runs. A Host outside the served
// set is a 403 before routing. Every request is logged once with its status
// and duration.
func (server *Server) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}

		writer.Header().Set(meta.AttributionHeader, meta.Attribution)

		if server.hosts[hostOnly(request.Host)] {
			server.mux.ServeHTTP(recorder, request)
		} else {
			writeError(recorder, http.StatusForbidden, codeForbidden,
				"requests must address the server as 127.0.0.1 or localhost")
		}

		server.logger.LogAttrs(request.Context(), slog.LevelInfo, "request",
			slog.String("method", request.Method),
			slog.String("path", request.URL.Path),
			slog.Int("status", recorder.status),
			slog.Int64("bytes", recorder.bytes),
			slog.Duration("duration", time.Since(started)),
		)
	})
}

// statusRecorder captures what the handler wrote, for the request log.
type statusRecorder struct {
	http.ResponseWriter

	status int
	bytes  int64
}

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(body []byte) (int, error) {
	written, err := recorder.ResponseWriter.Write(body)
	recorder.bytes += int64(written)

	if err != nil {
		return written, fmt.Errorf("write response: %w", err)
	}

	return written, nil
}
