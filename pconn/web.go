// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// webAssets holds the page that webServer serves. It is embedded, and the page
// refers to nothing outside itself, so that a running pconn needs no files
// alongside the binary and no network of its own to render it.
//
//go:embed index.html
var webAssets embed.FS

var webPage = template.Must(template.New("index.html").Funcs(template.FuncMap{
	"dict": webDict,
	"urls": webURLList,
}).ParseFS(webAssets, "index.html"))

// webDict builds the value for a part of the page that needs more than one,
// which the table of connections does: the connections to list, and whether
// the page can start and stop them.
func webDict(pairs ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			continue
		}
		out[key] = pairs[i+1]
	}
	return out
}

// webURLList joins the URLs of a group or connection for the attribute that
// carries them to the script, which splits them again. A URL has no spaces in
// it, which is what makes a space the separator.
func webURLList(urls []string) string {
	return strings.Join(urls, " ")
}

// webRefresh is how often the page brings itself up to date, which it does
// because the state it shows changes as connections are established and lost.
const webRefresh = 5 * time.Second

// pageSource is what the page is rendered from.
type pageSource interface {
	page() webPageData
}

// pageController is a source whose connections can be started and stopped
// whilst it runs, which is what 'pconn serve' serves. The page served by
// 'pconn connect' has no controller: that command's connections are fixed when
// it starts, so the page lists and reports on them but cannot change them.
type pageController interface {
	pageSource
	Start(targets ...string) error
	Stop(targets ...string) error
	Established(target string) (string, error)
}

// webServer serves the page that lists the connections, their groups and their
// local ports, so that what to connect to can be found, and started, without
// reading the configuration file.
type webServer struct {
	src      pageSource
	listener net.Listener
	logger   *slog.Logger
}

// listenWeb binds addr, so that an address that cannot be used is reported
// before any connection is made, as a port forward that cannot be bound is.
func listenWeb(addr string, src pageSource, logger *slog.Logger) (*webServer, error) {
	full := webAddr(addr)
	l, err := net.Listen("tcp", full)
	if err != nil {
		return nil, fmt.Errorf("cannot serve the connection list on %v: %w", full, err)
	}
	return &webServer{src: src, listener: l, logger: logger}, nil
}

// webAddr completes the address that the flag names: a bare port is served on
// the loopback interface, since the page names internal hosts and the ports
// that reach them and so is not for the rest of the network. An address that
// names an interface is served on it, which is the way to ask for that.
func webAddr(addr string) string {
	if !strings.Contains(addr, ":") {
		return net.JoinHostPort("127.0.0.1", addr)
	}
	return addr
}

// url is where the page is served, for the log line that says so.
func (s *webServer) url() string {
	return "http://" + s.listener.Addr().String() + "/"
}

// port is the port the page is served on, which is the one that was bound and
// so is a real port even where the address asked for any.
func (s *webServer) port() int {
	_, portStr, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

// checkForwardPorts reports a clash between the port the page is served on and
// a port that a connection forwards. Both bind that port on this host, so one
// of them would fail, and which one depends on the order they happen to start
// in: the forward is refused with EADDRINUSE if the page is already served, and
// the page cannot be served at all if the forward is up. It is checked against
// every connection in the configuration rather than only those that are
// running, so that a connection started later cannot collide with it either.
func (s *webServer) checkForwardPorts(cfg Config) error {
	port := s.port()
	if port == 0 {
		return nil
	}
	if name, ok := cfg.ForwardsLocalPort(port); ok {
		return fmt.Errorf("the list of connections cannot be served on port %d: connection %q forwards that port; serve it on another one with --http", port, name)
	}
	return nil
}

// close releases the address the page is served on, for a server that will not
// be served after all.
func (s *webServer) close() {
	_ = s.listener.Close()
}

// controller returns the source as one whose connections can be changed, and
// whether it is one.
func (s *webServer) controller() (pageController, bool) {
	c, ok := s.src.(pageController)
	return c, ok
}

// serve serves the page until ctx is done. It returns when the server has
// stopped, and reports a failure to serve rather than returning it: the page
// is a convenience, and losing it is no reason to stop maintaining the
// connections it describes.
func (s *webServer) serve(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/established", s.handleEstablished)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/stop", s.handleStop)
	srv := &http.Server{
		Handler: mux,
		// The page is read-only and served to this host, but a header that
		// never arrives would hold a connection open indefinitely.
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error("the connection list stopped being served", "url", s.url(), "err", err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx) // Close below ends anything it did not.
	_ = srv.Close()
	<-done
}

func (s *webServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	var buf bytes.Buffer
	if err := webPage.Execute(&buf, s.page()); err != nil {
		s.logger.Error("could not render the connection list", "err", err)
		http.Error(w, "could not render the connection list", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The state changes as connections come and go, so a cached copy of the
	// page would be worse than none.
	w.Header().Set("Cache-Control", "no-store")
	if _, err := buf.WriteTo(w); err != nil {
		s.logger.Error("could not write the connection list", "err", err)
	}
}

// handleState answers with the state the page shows, which it fetches to bring
// itself up to date and to wait for connections it has started.
func (s *webServer) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	s.writeJSON(w, http.StatusOK, s.page())
}

// handleEstablished answers whether every connection of a group or connection
// is established, which is what says when the ports it forwards can be used.
func (s *webServer) handleEstablished(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	c, ok := s.controller()
	if !ok {
		http.Error(w, "this page does not start connections", http.StatusNotFound)
		return
	}
	target := r.URL.Query().Get("target")
	waiting, err := c.Established(target)
	switch {
	case waiting == "" && err == nil:
		s.writeJSON(w, http.StatusOK, map[string]any{"ready": true})
	case err != nil:
		s.writeJSON(w, http.StatusOK, map[string]any{
			"ready": false, "waiting": waiting, "error": err.Error(),
		})
	default:
		s.writeJSON(w, http.StatusOK, map[string]any{"ready": false, "waiting": waiting})
	}
}

func (s *webServer) handleStart(w http.ResponseWriter, r *http.Request) {
	s.control(w, r, func(c pageController, targets []string) error { return c.Start(targets...) })
}

func (s *webServer) handleStop(w http.ResponseWriter, r *http.Request) {
	s.control(w, r, func(c pageController, targets []string) error { return c.Stop(targets...) })
}

// control runs one change to the connections on behalf of the page.
func (s *webServer) control(w http.ResponseWriter, r *http.Request, fn func(pageController, []string) error) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	c, ok := s.controller()
	if !ok {
		http.Error(w, "this page does not start or stop connections", http.StatusNotFound)
		return
	}
	if !fromPage(r) {
		http.Error(w, "this request did not come from the page", http.StatusForbidden)
		return
	}
	// An empty target is a request with nothing in it rather than a name that
	// cannot be resolved, which is a clearer thing to be told.
	targets := slices.DeleteFunc(r.URL.Query()["target"], func(t string) bool {
		return strings.TrimSpace(t) == ""
	})
	if len(targets) == 0 {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"error": "no group or connection was named"})
		return
	}
	if err := fn(c, targets); err != nil {
		s.logger.Warn("the page could not change the connections",
			"targets", strings.Join(targets, ", "), "err", err)
		s.writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	s.logger.Info("the page changed the connections",
		"request", r.URL.Path, "targets", strings.Join(targets, ", "))
	s.writeJSON(w, http.StatusOK, s.page())
}

// fromPage reports whether a request that changes the connections came from
// the page itself, which such a request has to. A page on another site can
// post a form to a loopback address, but it cannot set a header of its own
// without this server's consent, which it never gives, nor read the reply.
func fromPage(r *http.Request) bool {
	if r.Header.Get(webHeader) != "1" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	return true
}

// webHeader is the header that the page sets on every request that changes the
// connections, see fromPage.
const webHeader = "X-Pconn"

func (s *webServer) writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.logger.Error("could not write the response", "err", err)
	}
}

func (s *webServer) page() webPageData {
	data := s.src.page()
	data.RefreshSecs = int(webRefresh.Seconds())
	data.Generated = time.Now().Format("15:04:05")
	_, data.Interactive = s.controller()
	return data
}

// webForward is one forward of one connection: the local port to connect to,
// what answers on it, and what to open in a browser for it if anything.
type webForward struct {
	Local  int
	Target string
	URL    string
}

// webConn is one connection as the page shows it.
type webConn struct {
	Name     string
	Host     string
	User     string
	Proxy    string
	Tmux     string
	Status   string
	Detail   string
	Running  bool
	Forwards []webForward
	// URLs are the forwards' URLs, which is what a browser is pointed at when
	// the connection is started from the page.
	URLs []string
}

// webGroup is the connections of one group, or of none.
type webGroup struct {
	Name    string
	Conns   []webConn
	Running int
	URLs    []string
}

// webPageData is what the page is rendered from.
type webPageData struct {
	Interactive bool
	Groups      []webGroup
	Ungrouped   []webConn
	Total       int
	Up          int
	RefreshSecs int
	Generated   string
}

// webEntry is one connection and the groups it belongs to, which is what the
// page is grouped by.
type webEntry struct {
	conn   webConn
	groups []string
}

// webGrouped buckets the entries into their groups, in order, and returns the
// connections that belong to none alongside them. A connection in several
// groups appears under each of them.
func webGrouped(entries []webEntry) (groups []webGroup, ungrouped []webConn, total, up int) {
	byGroup := map[string][]webConn{}
	for _, e := range entries {
		total++
		if e.conn.Status == "up" {
			up++
		}
		if len(e.groups) == 0 {
			ungrouped = append(ungrouped, e.conn)
			continue
		}
		for _, g := range e.groups {
			byGroup[g] = append(byGroup[g], e.conn)
		}
	}
	names := make([]string, 0, len(byGroup))
	for name := range byGroup {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g := webGroup{Name: name, Conns: byGroup[name]}
		for _, c := range g.Conns {
			if c.Running {
				g.Running++
			}
			g.URLs = append(g.URLs, c.URLs...)
		}
		groups = append(groups, g)
	}
	return groups, ungrouped, total, up
}

// webConnFor describes one connection without its state, which differs between
// one that is running and one that is only configured.
func webConnFor(conn ConnectionConfig) webConn {
	c := webConn{
		Name: conn.Name,
		Host: conn.HostPort(),
		User: conn.ParsedUser(),
		Tmux: conn.SSH.Tmux.Command,
	}
	if hop, ok := conn.ProxyHost(); ok {
		c.Proxy = hop.Name
	}
	for _, f := range conn.SSH.PortForward.Forwards {
		fwd := webForward{Local: f.LocalPort(), Target: f.RemoteAddr()}
		if u, ok := f.BrowseURL(); ok {
			fwd.URL = u
			c.URLs = append(c.URLs, u)
		}
		c.Forwards = append(c.Forwards, fwd)
	}
	sort.Slice(c.Forwards, func(i, j int) bool { return c.Forwards[i].Local < c.Forwards[j].Local })
	return c
}

// page lists every connection in the configuration, with the state of those
// that are running, so that what is available can be started from the page and
// not only read from the configuration file.
func (s *supervisor) page() webPageData {
	byName := s.Status()
	names, byGroup, ungrouped := s.Groups()

	var entries []webEntry
	seen := map[string]bool{}
	add := func(conn ConnectionConfig, groups []string) {
		if seen[conn.Name] {
			return
		}
		seen[conn.Name] = true
		c := webConnFor(conn)
		st := byName[conn.Name]
		c.Running = st.Running
		switch {
		case st.Running:
			c.Status = st.State.Status
			c.Detail = webDetail(st.State)
		case st.Err != nil:
			c.Status = "failed"
			c.Detail = st.Err.Error()
		default:
			c.Status = "stopped"
		}
		entries = append(entries, webEntry{conn: c, groups: groups})
	}
	for _, name := range names {
		for _, conn := range byGroup[name] {
			add(conn, conn.SSH.PortForward.Groups)
		}
	}
	for _, conn := range ungrouped {
		add(conn, nil)
	}

	var data webPageData
	data.Groups, data.Ungrouped, data.Total, data.Up = webGrouped(entries)
	return data
}

// fixedConns is the page source for a command whose connections are fixed when
// it starts, which is 'pconn connect': the page lists them and reports on them
// but cannot start or stop them.
type fixedConns []*connRunner

func (f fixedConns) page() webPageData {
	entries := make([]webEntry, 0, len(f))
	for _, r := range f {
		c := webConnFor(r.conn)
		c.Running = true
		st := r.waiter.state()
		c.Status = st.Status
		c.Detail = webDetail(st)
		entries = append(entries, webEntry{conn: c, groups: r.conn.SSH.PortForward.Groups})
	}
	var data webPageData
	data.Groups, data.Ungrouped, data.Total, data.Up = webGrouped(entries)
	return data
}

// webDetail says what is known about a connection beyond its status: how long
// it has been waited for and why the last attempt failed, which is what
// distinguishes a connection that is coming up from one that needs something
// fixed.
func webDetail(st connState) string {
	if st.Status == "up" {
		if st.Establishments > 1 {
			return fmt.Sprintf("established %d times", st.Establishments)
		}
		return ""
	}
	var parts []string
	if st.Waiting > 0 {
		parts = append(parts, "waiting "+st.Waiting.Round(time.Second).String())
	}
	if st.Attempts > 0 {
		parts = append(parts, strconv.Itoa(st.Attempts)+" failed attempts")
	}
	if st.LastErr != nil {
		parts = append(parts, st.LastErr.Error())
	}
	return strings.Join(parts, ", ")
}
