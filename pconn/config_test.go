// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/cmdconfig"
	"cloudeng.io/cmdutil/cmdyaml"
	"cloudeng.io/net/pssh"
)

func TestConfigParseAndValidate(t *testing.T) {
	spec := `
logging:
  level: 2
  format: text
connections:
  - name: test-server
    host: remote.example.com:2222
    user: custom-user
    dial_timeout: 10s
    accept_new_host_keys: true
    known_hosts:
      - /path/to/known_hosts
    unlimited_retries: false
    forwards:
      - local_port: 8080
        remote_port: 80
      - local: 9090
        remote: 90
  - host: user2@db.example.com
    ssh_port: 2200
    forwards:
      - local_port: 5432
        remote_port: 5432
  - name: host-from-name.example.com
    forwards:
      - 7070:70
      - "7071:71"
`
	var cfg Config
	if err := cmdyaml.ParseConfigsStrict(&cfg, []byte(spec)); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	if len(cfg.Connections) != 3 {
		t.Fatalf("expected 3 connections, got %d", len(cfg.Connections))
	}

	c1 := cfg.Connections[0]
	if c1.Name != "test-server" {
		t.Errorf("got name %q, want %q", c1.Name, "test-server")
	}
	if got, want := c1.Type, "ssh"; got != want {
		t.Errorf("got type %q, want %q", got, want)
	}
	if got, want := c1.HostPort(), "remote.example.com:2222"; got != want {
		t.Errorf("got host:port %q, want %q", got, want)
	}
	if got, want := c1.ParsedUser(), "custom-user"; got != want {
		t.Errorf("got user %q, want %q", got, want)
	}
	if got, want := c1.DialTimeout, 10*time.Second; got != want {
		t.Errorf("got dial timeout %v, want %v", got, want)
	}
	if !c1.AcceptNewHostKeys {
		t.Errorf("got accept_new_host_keys false, want true")
	}
	if c1.ShouldUnlimitedRetries(true) {
		t.Errorf("got unlimited retries true, want false")
	}
	if len(c1.Forwards) != 2 {
		t.Fatalf("got %d forwards, want 2", len(c1.Forwards))
	}
	if c1.Forwards[0].LocalPort != 8080 || c1.Forwards[0].RemotePort != 80 {
		t.Errorf("forward 0: got %d:%d, want 8080:80", c1.Forwards[0].LocalPort, c1.Forwards[0].RemotePort)
	}
	if c1.Forwards[1].LocalPort != 9090 || c1.Forwards[1].RemotePort != 90 {
		t.Errorf("forward 1: got %d:%d, want 9090:90", c1.Forwards[1].LocalPort, c1.Forwards[1].RemotePort)
	}

	c2 := cfg.Connections[1]
	if got, want := c2.Name, "user2@db.example.com:2200"; got != want {
		t.Errorf("got auto-generated name %q, want %q", c2.Name, want)
	}
	if got, want := c2.HostPort(), "db.example.com:2200"; got != want {
		t.Errorf("got host:port %q, want %q", got, want)
	}
	if got, want := c2.ParsedUser(), "user2"; got != want {
		t.Errorf("got user %q, want %q", got, want)
	}
	if got, want := c2.DialTimeout, pssh.DefaultDialTimeout; got != want {
		t.Errorf("got default dial timeout %v, want %v", got, want)
	}
	if !c2.ShouldUnlimitedRetries(true) {
		t.Errorf("got unlimited retries false, want default true")
	}

	c3 := cfg.Connections[2]
	if got, want := c3.Name, "host-from-name.example.com"; got != want {
		t.Errorf("got name %q, want %q", c3.Name, want)
	}
	if got, want := c3.HostPort(), "host-from-name.example.com:22"; got != want {
		t.Errorf("got host:port %q, want %q (should use name as host)", got, want)
	}
	if len(c3.Forwards) != 2 {
		t.Fatalf("c3: got %d forwards, want 2", len(c3.Forwards))
	}
	if c3.Forwards[0].LocalPort != 7070 || c3.Forwards[0].RemotePort != 70 {
		t.Errorf("c3 forward 0: got %d:%d, want 7070:70", c3.Forwards[0].LocalPort, c3.Forwards[0].RemotePort)
	}
	if c3.Forwards[1].LocalPort != 7071 || c3.Forwards[1].RemotePort != 71 {
		t.Errorf("c3 forward 1: got %d:%d, want 7071:71", c3.Forwards[1].LocalPort, c3.Forwards[1].RemotePort)
	}
}

func TestParseHostUserPort(t *testing.T) {
	tests := []struct {
		raw         string
		defaultUser string
		defaultPort int
		wantUser    string
		wantHost    string
		wantPort    int
		wantErr     bool
	}{
		{raw: "example.com", defaultUser: "def", defaultPort: 22, wantUser: "def", wantHost: "example.com", wantPort: 22},
		{raw: "example.com:2222", defaultUser: "def", defaultPort: 22, wantUser: "def", wantHost: "example.com", wantPort: 2222},
		{raw: "admin@example.com", defaultUser: "def", defaultPort: 22, wantUser: "admin", wantHost: "example.com", wantPort: 22},
		{raw: "admin@example.com:2222", defaultUser: "def", defaultPort: 22, wantUser: "admin", wantHost: "example.com", wantPort: 2222},
		{raw: "[::1]:2222", defaultUser: "def", defaultPort: 22, wantUser: "def", wantHost: "::1", wantPort: 2222},
		{raw: "admin@[::1]:2222", defaultUser: "def", defaultPort: 22, wantUser: "admin", wantHost: "::1", wantPort: 2222},
		{raw: "admin@[::1]", defaultUser: "def", defaultPort: 22, wantUser: "admin", wantHost: "::1", wantPort: 22},
		{raw: "", defaultUser: "def", defaultPort: 22, wantErr: true},
		{raw: "user@", defaultUser: "def", defaultPort: 22, wantErr: true},
		{raw: "host:badport", defaultUser: "def", defaultPort: 22, wantErr: true},
		{raw: "host:0", defaultUser: "def", defaultPort: 22, wantErr: true},
		{raw: "host:99999", defaultUser: "def", defaultPort: 22, wantErr: true},
		{raw: "[::1:bad", defaultUser: "def", defaultPort: 22, wantErr: true},
	}

	for _, tc := range tests {
		u, h, p, err := parseHostUserPort(tc.raw, tc.defaultUser, tc.defaultPort)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseHostUserPort(%q) expected error, got user=%q host=%q port=%d", tc.raw, u, h, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHostUserPort(%q) unexpected error: %v", tc.raw, err)
			continue
		}
		if u != tc.wantUser || h != tc.wantHost || p != tc.wantPort {
			t.Errorf("parseHostUserPort(%q) = (%q, %q, %d), want (%q, %q, %d)",
				tc.raw, u, h, p, tc.wantUser, tc.wantHost, tc.wantPort)
		}
	}
}

func TestConfigValidationErrors(t *testing.T) {
	t.Run("empty connections", func(t *testing.T) {
		cfg := Config{}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "at least one connection") {
			t.Fatalf("expected error about missing connections, got: %v", err)
		}
	})

	t.Run("duplicate connection names", func(t *testing.T) {
		cfg := Config{
			Connections: []ConnectionConfig{
				{Name: "c1", Host: "host1.example.com"},
				{Name: "c1", Host: "host2.example.com"},
			},
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate connection name") {
			t.Fatalf("expected duplicate connection name error, got: %v", err)
		}
	})

	t.Run("duplicate local port forward within same connection", func(t *testing.T) {
		cfg := Config{
			Connections: []ConnectionConfig{
				{
					Name: "c1",
					Host: "host1.example.com",
					Forwards: []ForwardConfig{
						{LocalPort: 8080, RemotePort: 80},
						{LocalPort: 8080, RemotePort: 8080},
					},
				},
			},
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate local forward port") {
			t.Fatalf("expected duplicate local port error, got: %v", err)
		}
	})

	t.Run("conflicting local port forward across connections", func(t *testing.T) {
		cfg := Config{
			Connections: []ConnectionConfig{
				{
					Name: "c1",
					Host: "host1.example.com",
					Forwards: []ForwardConfig{
						{LocalPort: 8080, RemotePort: 80},
					},
				},
				{
					Name: "c2",
					Host: "host2.example.com",
					Forwards: []ForwardConfig{
						{LocalPort: 8080, RemotePort: 8088},
					},
				},
			},
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "conflict on local port") {
			t.Fatalf("expected conflict on local port error, got: %v", err)
		}
	})

	t.Run("invalid forward port", func(t *testing.T) {
		cfg := Config{
			Connections: []ConnectionConfig{
				{
					Name: "c1",
					Host: "host1.example.com",
					Forwards: []ForwardConfig{
						{LocalPort: 99999, RemotePort: 80},
					},
				},
			},
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "invalid local port") {
			t.Fatalf("expected invalid local port error, got: %v", err)
		}
	})

	t.Run("unsupported connection type", func(t *testing.T) {
		cfg := Config{
			Connections: []ConnectionConfig{
				{
					Name: "c1",
					Type: "wireguard",
					Host: "host1.example.com",
				},
			},
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported connection type") {
			t.Fatalf("expected unsupported connection type error, got: %v", err)
		}
	})

	t.Run("empty host and name", func(t *testing.T) {
		cfg := Config{
			Connections: []ConnectionConfig{
				{},
			},
		}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "either host or name must not be empty") {
			t.Fatalf("expected error for empty host and name, got: %v", err)
		}
	})
}

func TestForwardStringFormats(t *testing.T) {
	spec := `
connections:
  - name: test-server
    forwards:
      - 8080:80
      - "9090:90"
      - local_port: 7070
        remote_port: 70
`
	var cfg Config
	if err := cmdyaml.ParseConfigsStrict(&cfg, []byte(spec)); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if len(cfg.Connections[0].Forwards) != 3 {
		t.Fatalf("expected 3 forwards, got %d", len(cfg.Connections[0].Forwards))
	}
	fwds := cfg.Connections[0].Forwards
	if fwds[0].LocalPort != 8080 || fwds[0].RemotePort != 80 {
		t.Errorf("fwd 0: got %v, want 8080:80", fwds[0])
	}
	if fwds[1].LocalPort != 9090 || fwds[1].RemotePort != 90 {
		t.Errorf("fwd 1: got %v, want 9090:90", fwds[1])
	}
	if fwds[2].LocalPort != 7070 || fwds[2].RemotePort != 70 {
		t.Errorf("fwd 2: got %v, want 7070:70", fwds[2])
	}

	invalidSpecs := []string{
		`connections: [{name: t, forwards: ["invalid"]}]`,
		`connections: [{name: t, forwards: ["abc:80"]}]`,
		`connections: [{name: t, forwards: ["80:abc"]}]`,
		`connections: [{name: t, forwards: ["80:80:80"]}]`,
		`connections: [{name: t, forwards: [12345]}]`,
	}
	for _, inv := range invalidSpecs {
		var c Config
		if err := cmdyaml.ParseConfigsStrict(&c, []byte(inv)); err == nil {
			t.Errorf("expected parse error for %s, got nil", inv)
		}
	}
}

func TestFilterConnections(t *testing.T) {
	conns := []ConnectionConfig{
		{Name: "c1", Host: "h1"},
		{Name: "c2", Host: "h2"},
		{Name: "c3", Host: "h3"},
	}

	// No names -> all
	res, err := filterConnections(conns, nil)
	if err != nil || len(res) != 3 {
		t.Fatalf("expected all 3 connections, got %d, err: %v", len(res), err)
	}

	// Subset
	res, err = filterConnections(conns, []string{"c2", "c1"})
	if err != nil || len(res) != 2 || res[0].Name != "c2" || res[1].Name != "c1" {
		t.Fatalf("expected [c2, c1], got %v, err: %v", res, err)
	}

	// Unknown
	_, err = filterConnections(conns, []string{"unknown"})
	if err == nil || !strings.Contains(err.Error(), "connection(s) not found: unknown") {
		t.Fatalf("expected error with unknown name, got: %v", err)
	}
}

func TestContextWithConfig(t *testing.T) {
	ctx := context.Background()
	if _, ok := cmdutil.ContextValue[Config](ctx); ok {
		t.Fatal("expected no config in empty context")
	}

	cfg := Config{Connections: []ConnectionConfig{{Name: "test"}}}
	ctx = cmdutil.ContextWithValue(ctx, cfg)
	got, ok := cmdutil.ContextValue[Config](ctx)
	if !ok || len(got.Connections) != 1 || got.Connections[0].Name != "test" {
		t.Fatalf("failed to retrieve config from context: got %v, ok %v", got, ok)
	}
}

func TestParseConfigFiles(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test.yaml")
	content := `
connections:
  - name: my-conn
    host: 127.0.0.1:2222
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := cmdyaml.ParseConfigFilesStrict(t.Context(), &cfg, cfgPath); err != nil {
		t.Fatalf("unexpected error parsing file: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error validating: %v", err)
	}
	if len(cfg.Connections) != 1 || cfg.Connections[0].Name != "my-conn" {
		t.Fatalf("unexpected config content: %v", cfg)
	}
}

// exampleConfigFile is the example configuration shipped with pconn. The
// tests parse it so that the example cannot drift from what the parser and
// validator actually accept.
const exampleConfigFile = "testdata/pconn.yaml"

// parseExampleConfig parses and validates the example configuration,
// requiring it to be accepted in strict mode, ie. with no field in it that
// Config does not define.
func parseExampleConfig(t *testing.T) Config {
	t.Helper()
	var cfg Config
	if err := cmdyaml.ParseConfigFilesStrict(t.Context(), &cfg, exampleConfigFile); err != nil {
		t.Fatalf("failed to parse %v: %v", exampleConfigFile, err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("%v failed validation: %v", exampleConfigFile, err)
	}
	return cfg
}

func testExampleConfigConnections(t *testing.T) {
	cfg := parseExampleConfig(t)
	if got, want := len(cfg.Connections), 3; got != want {
		t.Fatalf("got %v connections, want %v", got, want)
	}
	for i, tc := range []struct {
		name, hostPort, user string
		dialTimeout          time.Duration
		forwards             []string
	}{
		{"dev-web", "web.dev.example.com:22", "admin", 30 * time.Second, []string{"8080:80", "8443:443"}},
		// host is taken from the name, which also supplies the port.
		{"db.dev.example.com:2222", "db.dev.example.com:2222", "dba", 15 * time.Second, []string{"5432:5432"}},
		// type, port and dial_timeout are all defaulted.
		{"shared-cache", "redis.internal.example.com:22", "", pssh.DefaultDialTimeout, []string{"6379:6379"}},
	} {
		conn := cfg.Connections[i]
		if got, want := conn.Name, tc.name; got != want {
			t.Errorf("connection #%d: got name %q, want %q", i+1, got, want)
		}
		if got, want := conn.HostPort(), tc.hostPort; got != want {
			t.Errorf("%v: got %q, want %q", tc.name, got, want)
		}
		// Every connection defaults to, or states, the only supported type.
		if got, want := conn.Type, "ssh"; got != want {
			t.Errorf("%v: got type %q, want %q", tc.name, got, want)
		}
		if got, want := conn.DialTimeout, tc.dialTimeout; got != want {
			t.Errorf("%v: got dial timeout %v, want %v", tc.name, got, want)
		}
		// A connection that names no user inherits the one running pconn,
		// so only an explicit user can be checked for.
		if tc.user != "" {
			if got, want := conn.ParsedUser(), tc.user; got != want {
				t.Errorf("%v: got user %q, want %q", tc.name, got, want)
			}
		}
		var forwards []string
		for _, f := range conn.Forwards {
			forwards = append(forwards, f.String())
		}
		if !slices.Equal(forwards, tc.forwards) {
			t.Errorf("%v: got forwards %v, want %v", tc.name, forwards, tc.forwards)
		}
	}
}

func testExampleConfigGroups(t *testing.T) {
	cfg := parseExampleConfig(t)
	if got, want := cfg.Groups(), []string{"backend", "dev", "frontend", "staging"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, tc := range []struct {
		group string
		want  []string
	}{
		{"dev", []string{"dev-web", "db.dev.example.com:2222"}},
		{"frontend", []string{"dev-web"}},
		{"backend", []string{"db.dev.example.com:2222", "shared-cache"}},
		{"staging", []string{"shared-cache"}},
		// Every connection in the example belongs to a group, so "all"
		// selects them all and none are left ungrouped.
		{"all", []string{"dev-web", "db.dev.example.com:2222", "shared-cache"}},
	} {
		conns, err := cfg.ConnectionsForGroup(tc.group)
		if err != nil {
			t.Errorf("%v: %v", tc.group, err)
			continue
		}
		var names []string
		for _, c := range conns {
			names = append(names, c.Name)
		}
		if !slices.Equal(names, tc.want) {
			t.Errorf("%v: got %v, want %v", tc.group, names, tc.want)
		}
	}
	if got := cfg.ConnectionsWithoutGroup(); len(got) != 0 {
		t.Errorf("got %v, want no ungrouped connections", got)
	}
}

func testExampleConfigPortsAndRetries(t *testing.T) {
	cfg := parseExampleConfig(t)
	// Validate has already checked for conflicts within each group; the
	// example's forwards are distinct across every connection.
	if port, first, second, conflict := cfg.HasConflictingPortsAcrossAll(); conflict {
		t.Errorf("port %v is forwarded by both %v and %v", port, first, second)
	}
	// unlimited_retries is unset for dev-web, so it follows the default
	// either way, and explicitly false for the second connection, which
	// overrides the default.
	devWeb, db := cfg.Connections[0], cfg.Connections[1]
	for _, dflt := range []bool{true, false} {
		if got, want := devWeb.ShouldUnlimitedRetries(dflt), dflt; got != want {
			t.Errorf("%v: with default %v: got %v, want %v", devWeb.Name, dflt, got, want)
		}
		if got := db.ShouldUnlimitedRetries(dflt); got {
			t.Errorf("%v: with default %v: got %v, want false", db.Name, dflt, got)
		}
	}
}

// TestExampleConfig covers the example configuration file, which doubles as
// documentation: it must parse in strict mode, validate, and resolve to the
// values its comments claim for it.
func TestExampleConfig(t *testing.T) {
	t.Run("connections", testExampleConfigConnections)
	t.Run("groups", testExampleConfigGroups)
	t.Run("ports and retries", testExampleConfigPortsAndRetries)
}

func TestConnectionGroups(t *testing.T) {
	spec := `
connections:
  - name: web
    host: web.example.com
    groups:
      - dev
      - frontend
    forwards:
      - local_port: 8080
        remote_port: 80
  - name: db
    host: db.example.com
    groups:
      - dev
      - backend
    forwards:
      - local_port: 5432
        remote_port: 5432
  - name: cache
    host: cache.example.com
    groups:
      - backend
    forwards:
      - local_port: 6379
        remote_port: 6379
  - name: staging-db
    host: db.staging.example.com
    groups:
      - staging
    forwards:
      - local_port: 5432
        remote_port: 5432
`
	var cfg Config
	if err := cmdyaml.ParseConfigsStrict(&cfg, []byte(spec)); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	// Should validate successfully because staging-db and db share local port 5432,
	// but belong to disjoint groups ("staging" vs "dev"/"backend")!
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	// Test Groups()
	groups := cfg.Groups()
	expectedGroups := []string{"backend", "dev", "frontend", "staging"}
	if len(groups) != len(expectedGroups) {
		t.Fatalf("got groups %v, want %v", groups, expectedGroups)
	}
	for i := range groups {
		if groups[i] != expectedGroups[i] {
			t.Errorf("group %d: got %q, want %q", i, groups[i], expectedGroups[i])
		}
	}

	// Test ConnectionsForGroup("dev") -> [web, db]
	devConns, err := cfg.ConnectionsForGroup("dev")
	if err != nil || len(devConns) != 2 {
		t.Fatalf("expected 2 connections for 'dev', got %d (err: %v)", len(devConns), err)
	}
	if devConns[0].Name != "web" || devConns[1].Name != "db" {
		t.Errorf("unexpected dev connections: %v", devConns)
	}

	// Test ConnectionsForGroup("backend") -> [db, cache] (db belongs to both dev and backend)
	backendConns, err := cfg.ConnectionsForGroup("backend")
	if err != nil || len(backendConns) != 2 {
		t.Fatalf("expected 2 connections for 'backend', got %d (err: %v)", len(backendConns), err)
	}
	if backendConns[0].Name != "db" || backendConns[1].Name != "cache" {
		t.Errorf("unexpected backend connections: %v", backendConns)
	}

	// Test ConnectionsForGroup("staging") -> [staging-db]
	stagingConns, err := cfg.ConnectionsForGroup("staging")
	if err != nil || len(stagingConns) != 1 || stagingConns[0].Name != "staging-db" {
		t.Fatalf("expected [staging-db] for 'staging', got %v (err: %v)", stagingConns, err)
	}

	// Test ConnectionsForGroup("all") -> all 4 connections
	allConns, err := cfg.ConnectionsForGroup("all")
	if err != nil || len(allConns) != 4 {
		t.Fatalf("expected 4 connections for 'all', got %d (err: %v)", len(allConns), err)
	}

	// Test ConnectionsForGroups("dev", "backend") -> [web, db, cache] (deduplicated)
	multiConns, err := cfg.ConnectionsForGroups("dev", "backend")
	if err != nil || len(multiConns) != 3 {
		t.Fatalf("expected 3 deduplicated connections for 'dev' + 'backend', got %d (err: %v)", len(multiConns), err)
	}
	if multiConns[0].Name != "web" || multiConns[1].Name != "db" || multiConns[2].Name != "cache" {
		t.Errorf("unexpected multi-group connections: %v", multiConns)
	}

	// Test ConnectionsForGroup("unknown")
	_, err = cfg.ConnectionsForGroup("unknown")
	if err == nil || !strings.Contains(err.Error(), "group \"unknown\" not found") {
		t.Fatalf("expected error for unknown group, got: %v", err)
	}

	// Test HasConflictingPortsAcrossAll reports conflict (port 5432 between db and staging-db)
	port, c1, c2, conflict := cfg.HasConflictingPortsAcrossAll()
	if !conflict || port != 5432 {
		t.Errorf("expected conflict on port 5432, got conflict=%v port=%d (%s vs %s)", conflict, port, c1, c2)
	}
}

func TestGroupPortConflictValidation(t *testing.T) {
	// Two connections in the SAME group with conflicting local ports
	spec := `
connections:
  - name: c1
    host: h1.example.com
    groups:
      - dev
    forwards:
      - local_port: 8080
        remote_port: 80
  - name: c2
    host: h2.example.com
    groups:
      - dev
    forwards:
      - local_port: 8080
        remote_port: 8080
`
	var cfg Config
	if err := cmdyaml.ParseConfigsStrict(&cfg, []byte(spec)); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "group \"dev\": conflict on local port 8080") {
		t.Fatalf("expected conflict on local port 8080 within group 'dev', got: %v", err)
	}
}

// TestConfigFileNames covers the names reported when a configuration fails
// validation. Nothing having been located is not the same as nothing having
// been asked for, and an error that names no file at all is of little use.
func TestConfigFileNames(t *testing.T) {
	located := &cmdconfig.Located{YAML: []string{"/etc/pconn/config.yaml"}}
	twoLocated := &cmdconfig.Located{YAML: []string{"a.yaml", "b.yaml"}}

	var flagWithFile cmdconfig.FilesFlag
	if err := flagWithFile.Set("asked-for.yaml"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var emptyFlag cmdconfig.FilesFlag

	for _, tc := range []struct {
		name  string
		loc   *cmdconfig.Located
		files *cmdconfig.FilesFlag
		want  string
	}{
		// What was read is what the error should name.
		{"located", located, &flagWithFile, "/etc/pconn/config.yaml"},
		{"several located", twoLocated, &flagWithFile, "a.yaml, b.yaml"},
		// Nothing located: the flag says what was asked for.
		{"nothing located", &cmdconfig.Located{}, &flagWithFile, "asked-for.yaml"},
		{"no location at all", nil, &flagWithFile, "asked-for.yaml"},
		// Neither: say so rather than quoting an empty string.
		{"neither", &cmdconfig.Located{}, &emptyFlag, "(none)"},
		{"neither, no flag", nil, nil, "(none)"},
	} {
		if got := configFileNames(tc.loc, tc.files); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
