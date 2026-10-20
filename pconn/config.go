// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/net/pssh"
	"gopkg.in/yaml.v3"
)

// ForwardConfig specifies a local TCP port forward over a connection.
// In YAML, it can be specified as a string "<local>:<remote>" (e.g. "8080:80")
// or as a mapping with local_port/remote_port fields.
type ForwardConfig struct {
	LocalPort  int `yaml:"local_port,omitempty" doc:"local port to bind (alias: local)"`
	RemotePort int `yaml:"remote_port,omitempty" doc:"remote port to forward to (alias: remote)"`
	Local      int `yaml:"local,omitempty" doc:"local port (alias for local_port)"`
	Remote     int `yaml:"remote,omitempty" doc:"remote port (alias for remote_port)"`
}

func (f *ForwardConfig) parseString(s string) error {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid forward format %q: expected <local>:<remote>", s)
	}
	lpStr := strings.TrimSpace(parts[0])
	rpStr := strings.TrimSpace(parts[1])
	lp, err := strconv.Atoi(lpStr)
	if err != nil {
		return fmt.Errorf("invalid local port %q in forward %q: %w", lpStr, s, err)
	}
	rp, err := strconv.Atoi(rpStr)
	if err != nil {
		return fmt.Errorf("invalid remote port %q in forward %q: %w", rpStr, s, err)
	}
	f.LocalPort = lp
	f.RemotePort = rp
	return nil
}

// UnmarshalYAML decodes either a string "<local>:<remote>" or a mapping.
func (f *ForwardConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return f.parseString(value.Value)
	}
	if value.Kind == yaml.MappingNode {
		type plain ForwardConfig
		var p plain
		if err := value.Decode(&p); err != nil {
			return err
		}
		*f = ForwardConfig(p)
		return nil
	}
	return fmt.Errorf("forward specification must be <local>:<remote> string or map, got yaml kind %d", value.Kind)
}

// MarshalYAML encodes ForwardConfig as "<local>:<remote>".
func (f ForwardConfig) MarshalYAML() (any, error) {
	return f.String(), nil
}

// String returns "<local>:<remote>".
func (f ForwardConfig) String() string {
	return fmt.Sprintf("%d:%d", f.LocalPort, f.RemotePort)
}

// Validate normalizes aliases and checks port bounds.
func (f *ForwardConfig) Validate() error {
	if f.LocalPort == 0 && f.Local != 0 {
		f.LocalPort = f.Local
	}
	if f.RemotePort == 0 && f.Remote != 0 {
		f.RemotePort = f.Remote
	}
	if f.LocalPort < 1 || f.LocalPort > 65535 {
		return fmt.Errorf("invalid local port %d: must be between 1 and 65535", f.LocalPort)
	}
	if f.RemotePort < 1 || f.RemotePort > 65535 {
		return fmt.Errorf("invalid remote port %d: must be between 1 and 65535", f.RemotePort)
	}
	return nil
}

// ConnectionConfig defines a single persistent connection and its port forwards.
type ConnectionConfig struct {
	Name              string          `yaml:"name,omitempty" doc:"unique name for the connection; defaults to user@host:port"`
	Type              string          `yaml:"type,omitempty" doc:"connection type/protocol (e.g. ssh); defaults to ssh"`
	Host              string          `yaml:"host,omitempty" doc:"remote host; if omitted, defaults to name"`
	Port              int             `yaml:"port,omitempty" doc:"remote port (alias for ssh_port)"`
	SSHPort           int             `yaml:"ssh_port,omitempty" doc:"SSH port on remote host; defaults to 22 if not in host"`
	User              string          `yaml:"user,omitempty" doc:"username; defaults to current user"`
	DialTimeout       time.Duration   `yaml:"dial_timeout,omitempty" doc:"timeout for establishing connection; defaults to 30s"`
	AcceptNewHostKeys bool            `yaml:"accept_new_host_keys,omitempty" doc:"accept and record new SSH host keys"`
	KnownHosts        []string        `yaml:"known_hosts,omitempty" doc:"custom known_hosts files for host verification"`
	Forwards          []ForwardConfig `yaml:"forwards,omitempty" doc:"local port forwards to establish (<local>:<remote>)"`
	UnlimitedRetries  *bool           `yaml:"unlimited_retries,omitempty" doc:"continue retrying indefinitely; defaults to true"`
	Groups            []string        `yaml:"groups,omitempty" doc:"groups this connection belongs to"`

	parsedHost string
	parsedPort int
	parsedUser string
}

// InGroup reports whether this connection belongs to the specified group.
func (c *ConnectionConfig) InGroup(group string) bool {
	for _, g := range c.Groups {
		if g == group {
			return true
		}
	}
	return false
}

// HostPort returns the normalized host:port string for dialling.
func (c *ConnectionConfig) HostPort() string {
	return net.JoinHostPort(c.parsedHost, strconv.Itoa(c.parsedPort))
}

// ParsedUser returns the effective SSH username.
func (c *ConnectionConfig) ParsedUser() string {
	return c.parsedUser
}

// ShouldUnlimitedRetries reports whether this connection should retry indefinitely.
func (c *ConnectionConfig) ShouldUnlimitedRetries(defaultUnlimited bool) bool {
	if c.UnlimitedRetries != nil {
		return *c.UnlimitedRetries
	}
	return defaultUnlimited
}

// Validate validates and normalizes the connection configuration.
func (c *ConnectionConfig) Validate() error {
	trimmedHost := strings.TrimSpace(c.Host)
	trimmedName := strings.TrimSpace(c.Name)
	if trimmedHost == "" {
		if trimmedName == "" {
			return fmt.Errorf("either host or name must not be empty")
		}
		trimmedHost = trimmedName
		c.Host = trimmedName
	}

	defaultPort := 22
	if c.SSHPort > 0 {
		defaultPort = c.SSHPort
	} else if c.Port > 0 {
		defaultPort = c.Port
	}

	user, host, port, err := parseHostUserPort(trimmedHost, c.User, defaultPort)
	if err != nil {
		return err
	}

	c.parsedHost = host
	c.parsedPort = port
	c.parsedUser = user
	if c.parsedUser == "" {
		c.parsedUser = os.Getenv("USER")
	}

	if c.Name == "" {
		if c.parsedUser != "" {
			c.Name = fmt.Sprintf("%s@%s:%d", c.parsedUser, c.parsedHost, c.parsedPort)
		} else {
			c.Name = fmt.Sprintf("%s:%d", c.parsedHost, c.parsedPort)
		}
	}

	if c.Type == "" {
		c.Type = "ssh"
	}
	switch strings.ToLower(c.Type) {
	case "ssh":
		c.Type = "ssh"
	default:
		return fmt.Errorf("connection %q: unsupported connection type %q (supported types: ssh)", c.Name, c.Type)
	}

	if c.DialTimeout <= 0 {
		c.DialTimeout = pssh.DefaultDialTimeout
	}

	localPorts := make(map[int]bool)
	for i := range c.Forwards {
		if err := c.Forwards[i].Validate(); err != nil {
			return fmt.Errorf("connection %q forward #%d: %w", c.Name, i+1, err)
		}
		lp := c.Forwards[i].LocalPort
		if localPorts[lp] {
			return fmt.Errorf("connection %q: duplicate local forward port %d", c.Name, lp)
		}
		localPorts[lp] = true
	}

	return nil
}

func parseHostUserPort(raw string, defaultUser string, defaultPort int) (user, host string, port int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", 0, fmt.Errorf("host must not be empty")
	}

	user = defaultUser
	port = defaultPort

	if atIdx := strings.Index(raw, "@"); atIdx != -1 {
		user = raw[:atIdx]
		raw = raw[atIdx+1:]
		if user == "" {
			user = defaultUser
		}
		if raw == "" {
			return "", "", 0, fmt.Errorf("missing host after '@'")
		}
	}

	if strings.HasPrefix(raw, "[") {
		closeIdx := strings.Index(raw, "]")
		if closeIdx == -1 {
			return "", "", 0, fmt.Errorf("unmatched '[' in IPv6 address %q", raw)
		}
		host = raw[1:closeIdx]
		if host == "" {
			return "", "", 0, fmt.Errorf("empty IPv6 address inside brackets")
		}
		suffix := raw[closeIdx+1:]
		if suffix != "" {
			if !strings.HasPrefix(suffix, ":") {
				return "", "", 0, fmt.Errorf("expected ':' after IPv6 bracket in %q", raw)
			}
			p, err := strconv.Atoi(suffix[1:])
			if err != nil || p < 1 || p > 65535 {
				return "", "", 0, fmt.Errorf("invalid port in %q: %v", raw, suffix[1:])
			}
			port = p
		}
		return user, host, port, nil
	}

	if strings.Contains(raw, ":") {
		if strings.Count(raw, ":") > 1 {
			host = raw
			return user, host, port, nil
		}
		h, pStr, err := net.SplitHostPort(raw)
		if err == nil {
			p, err := strconv.Atoi(pStr)
			if err != nil || p < 1 || p > 65535 {
				return "", "", 0, fmt.Errorf("invalid port in %q: %v", raw, pStr)
			}
			return user, h, p, nil
		}
	}

	host = raw
	if port < 1 || port > 65535 {
		return "", "", 0, fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}
	return user, host, port, nil
}

// Config represents the common YAML configuration file for pconn.
type Config struct {
	Logging     cmdutil.LoggingConfig `yaml:"logging,omitempty" doc:"logging configuration"`
	Connections []ConnectionConfig    `yaml:"connections" doc:"list of persistent connections to maintain"`
}

// Groups returns a sorted list of all unique group names across all connections.
func (cfg *Config) Groups() []string {
	seen := make(map[string]bool)
	var groups []string
	for _, c := range cfg.Connections {
		for _, g := range c.Groups {
			if !seen[g] {
				seen[g] = true
				groups = append(groups, g)
			}
		}
	}
	slices.Sort(groups)
	return groups
}

// ConnectionsForGroup returns all connections that belong to the specified group.
// The special group name "all" returns all configured connections.
func (cfg *Config) ConnectionsForGroup(group string) ([]ConnectionConfig, error) {
	if group == "all" {
		if len(cfg.Connections) == 0 {
			return nil, fmt.Errorf("no connections defined in configuration")
		}
		return slices.Clone(cfg.Connections), nil
	}
	var matched []ConnectionConfig
	for _, c := range cfg.Connections {
		if c.InGroup(group) {
			matched = append(matched, c)
		}
	}
	if len(matched) == 0 {
		available := cfg.Groups()
		if len(available) == 0 {
			return nil, fmt.Errorf("group %q not found: no groups defined in configuration", group)
		}
		return nil, fmt.Errorf("group %q not found in configuration (available groups: %s)", group, strings.Join(available, ", "))
	}
	return matched, nil
}

// ConnectionsForGroups returns the deduplicated list of connections belonging
// to any of the specified groups, preserving their order of appearance.
// The special group name "all" selects all configured connections.
func (cfg *Config) ConnectionsForGroups(groups ...string) ([]ConnectionConfig, error) {
	seen := make(map[string]bool)
	var result []ConnectionConfig
	for _, g := range groups {
		conns, err := cfg.ConnectionsForGroup(g)
		if err != nil {
			return nil, err
		}
		for _, c := range conns {
			if !seen[c.Name] {
				seen[c.Name] = true
				result = append(result, c)
			}
		}
	}
	return result, nil
}

// ConnectionsWithoutGroup returns all connections that do not belong to any group.
func (cfg *Config) ConnectionsWithoutGroup() []ConnectionConfig {
	var result []ConnectionConfig
	for _, c := range cfg.Connections {
		if len(c.Groups) == 0 {
			result = append(result, c)
		}
	}
	return result
}

// HasConflictingPorts reports whether any two connections in conns share a local port forward.
func HasConflictingPorts(conns []ConnectionConfig) (int, string, string, bool) {
	ports := make(map[int]string)
	for _, c := range conns {
		for _, f := range c.Forwards {
			if prev, exists := ports[f.LocalPort]; exists {
				return f.LocalPort, prev, c.Name, true
			}
			ports[f.LocalPort] = c.Name
		}
	}
	return 0, "", "", false
}

// HasConflictingPortsAcrossAll reports whether any two connections share a local port forward.
func (cfg *Config) HasConflictingPortsAcrossAll() (int, string, string, bool) {
	return HasConflictingPorts(cfg.Connections)
}

// Validate validates the entire configuration.
func (cfg *Config) Validate() error {
	if len(cfg.Connections) == 0 {
		return fmt.Errorf("at least one connection must be specified in the configuration")
	}

	names := make(map[string]bool)
	for i := range cfg.Connections {
		c := &cfg.Connections[i]
		if err := c.Validate(); err != nil {
			return fmt.Errorf("connection #%d: %w", i+1, err)
		}
		if names[c.Name] {
			return fmt.Errorf("duplicate connection name %q", c.Name)
		}
		names[c.Name] = true
	}

	groups := cfg.Groups()
	if len(groups) == 0 {
		allLocalPorts := make(map[int]string)
		for _, c := range cfg.Connections {
			for _, f := range c.Forwards {
				if prevConn, exists := allLocalPorts[f.LocalPort]; exists {
					return fmt.Errorf("conflict on local port %d: used by both connection %q and %q", f.LocalPort, prevConn, c.Name)
				}
				allLocalPorts[f.LocalPort] = c.Name
			}
		}
		return nil
	}

	for _, group := range groups {
		localPorts := make(map[int]string)
		for _, c := range cfg.Connections {
			if c.InGroup(group) {
				for _, f := range c.Forwards {
					if prevConn, exists := localPorts[f.LocalPort]; exists {
						return fmt.Errorf("group %q: conflict on local port %d between connection %q and %q",
							group, f.LocalPort, prevConn, c.Name)
					}
					localPorts[f.LocalPort] = c.Name
				}
			}
		}
	}

	return nil
}

type configContextKey struct{}

// ContextWithConfig returns a context carrying the parsed Config.
func ContextWithConfig(ctx context.Context, cfg Config) context.Context {
	return context.WithValue(ctx, configContextKey{}, cfg)
}

// ConfigFromContext extracts the Config from the context.
func ConfigFromContext(ctx context.Context) (Config, bool) {
	cfg, ok := ctx.Value(configContextKey{}).(Config)
	return cfg, ok
}
