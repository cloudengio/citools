// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net"
	"os"
	"reflect"
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

// PortForwardConfig specifies the local port forwards that 'pconn connect'
// maintains over a connection.
type PortForwardConfig struct {
	Forwards         []ForwardConfig `yaml:"forwards,omitempty" doc:"local port forwards to establish, as <local>:<remote>"`
	UnlimitedRetries *bool           `yaml:"unlimited_retries,omitempty" doc:"continue retrying indefinitely; defaults to true"`
	Groups           []string        `yaml:"groups,omitempty" doc:"groups that this connection belongs to, which are selected when running 'pconn connect <group>'"`
	ForwardAgent     bool            `yaml:"forward_agent,omitempty" doc:"make the local ssh agent available to the remote host; since pconn connect runs no commands on it this has no effect on its own"`
}

// TmuxConfig specifies how 'pconn tmux' runs tmux over a connection.
type TmuxConfig struct {
	Command      string `yaml:"command,omitempty" doc:"the complete command line to run on the remote host using the login shell, eg. /opt/homebrew/bin/tmux -CC new -A -s main; required by pconn tmux"`
	ForwardAgent bool   `yaml:"forward_agent,omitempty" doc:"make the local ssh agent available to tmux and the commands run within it; only for hosts that are trusted"`
}

// Validate checks the tmux configuration.
func (t *TmuxConfig) Validate() error {
	if t.Command != "" && strings.TrimSpace(t.Command) == "" {
		return fmt.Errorf("tmux command must not be blank")
	}
	return nil
}

// SSHConfig specifies an ssh connection to a host, and what is done over it:
// maintain port forwards, and/or run tmux.
type SSHConfig struct {
	Port              int               `yaml:"port,omitempty" doc:"remote port (alias for ssh_port)"`
	SSHPort           int               `yaml:"ssh_port,omitempty" doc:"SSH port on the remote host; defaults to 22 if not in host"`
	User              string            `yaml:"user,omitempty" doc:"username; defaults to the current user"`
	DialTimeout       time.Duration     `yaml:"dial_timeout,omitempty" doc:"timeout for establishing a connection; defaults to 30s"`
	AcceptNewHostKeys bool              `yaml:"accept_new_host_keys,omitempty" doc:"accept and record new SSH host keys"`
	KnownHosts        []string          `yaml:"known_hosts,omitempty" doc:"custom known_hosts files for host verification"`
	Proxy             string            `yaml:"proxy,omitempty" doc:"reach this host through another one, as ssh's ProxyJump ('ssh -J') does, eg. for a host behind a bastion: either the name of another connection, which says how that host is reached, or host, user@host or user@host:port for one that has no connection of its own; a proxy host must itself be reachable directly"`
	PortForward       PortForwardConfig `yaml:"port_forward,omitempty" doc:"port forwards maintained by 'pconn connect'"`
	Tmux              TmuxConfig        `yaml:"tmux,omitempty" doc:"how 'pconn tmux <name>' runs tmux"`

	parsedHost string
	parsedPort int
	parsedUser string
}

// ConnectionConfig defines a persistent connection. The type of the connection
// is that of the one block of type specific fields that is set; ssh is the only
// type at present. If Type is given but its block is not, the block is set to
// its defaults.
type ConnectionConfig struct {
	Name string `yaml:"name,omitempty" doc:"unique name for the connection; defaults to user@host:port"`
	// Type is optional, since it is implied by the block that is set, but if
	// specified it must name that block.
	Type string `yaml:"type,omitempty" doc:"connection type, eg. ssh; optional since it is implied by which of the type specific blocks is set, exactly one of which must be"`
	Host string `yaml:"host,omitempty" doc:"the remote host as a hostname or an IPv4 or IPv6 address, eg. example.com, 192.0.2.1 or [2001:db8::1]; if omitted, defaults to the connection's name. For ssh it may also include a user and port, as user@host:port"`
	// The blocks of fields specific to each type of connection.
	SSH *SSHConfig `yaml:"ssh,omitempty" doc:"fields for a connection of type ssh"`

	// proxy is the connection that this one is reached through, resolved from
	// SSH.Proxy by Config.Validate, and usedAsProxy says that another connection
	// is reached through this one. See ProxyHost.
	proxy       *ConnectionConfig
	usedAsProxy bool
}

// ProxyHost returns the connection that this one is reached through, and
// whether there is one. The connection is returned by value: it is a part of
// the configuration and is not the caller's to change.
func (c *ConnectionConfig) ProxyHost() (ConnectionConfig, bool) {
	if c.proxy == nil {
		return ConnectionConfig{}, false
	}
	return *c.proxy, true
}

// connectionType returns the type of the connection, which is that of the
// type specific block that is set. It is an error for there to be other than
// exactly one, or for a specified Type to name a different one.
func (c *ConnectionConfig) connectionType() (string, error) {
	var set []string
	if c.SSH != nil {
		set = append(set, "ssh")
	}
	switch len(set) {
	case 0:
		switch {
		case strings.EqualFold(c.Type, "ssh"):
			// The type alone is enough when there is nothing else to say.
			c.SSH = &SSHConfig{}
			return "ssh", nil
		case c.Type != "":
			return "", fmt.Errorf("unsupported connection type %q (supported types: ssh)", c.Type)
		}
		return "", fmt.Errorf("the type must be specified, or the block of fields for it be set, eg. ssh")
	case 1:
		if c.Type != "" && !strings.EqualFold(c.Type, set[0]) {
			return "", fmt.Errorf("type %q does not match the %s block", c.Type, set[0])
		}
		return set[0], nil
	}
	return "", fmt.Errorf("only one of the blocks %s may be set", strings.Join(set, ", "))
}

// MaintainedByConnect reports whether 'pconn connect' maintains the connection,
// which is so unless the connection only configures tmux, or exists only to be
// proxyed through: a connection that has neither port forwards nor tmux of its
// own but which another is reached through is maintained for as long as that
// one is, and so is not a connection to select in its own right.
func (c *ConnectionConfig) MaintainedByConnect() bool {
	noWorkOfItsOwn := reflect.ValueOf(c.SSH.PortForward).IsZero() && reflect.ValueOf(c.SSH.Tmux).IsZero()
	if c.usedAsProxy && noWorkOfItsOwn {
		return false
	}
	return !reflect.ValueOf(c.SSH.PortForward).IsZero() || reflect.ValueOf(c.SSH.Tmux).IsZero()
}

// InGroup reports whether this connection belongs to the specified group.
func (c *ConnectionConfig) InGroup(group string) bool {
	return slices.Contains(c.SSH.PortForward.Groups, group)
}

// HostPort returns the normalized host:port string for dialling.
func (c *ConnectionConfig) HostPort() string {
	return net.JoinHostPort(c.SSH.parsedHost, strconv.Itoa(c.SSH.parsedPort))
}

// ParsedUser returns the effective SSH username.
func (c *ConnectionConfig) ParsedUser() string {
	return c.SSH.parsedUser
}

// ShouldUnlimitedRetries reports whether this connection should retry indefinitely.
func (c *ConnectionConfig) ShouldUnlimitedRetries(defaultUnlimited bool) bool {
	if c.SSH.PortForward.UnlimitedRetries != nil {
		return *c.SSH.PortForward.UnlimitedRetries
	}
	return defaultUnlimited
}

// Validate validates and normalizes the connection configuration.
func (c *ConnectionConfig) Validate() error {
	typ, err := c.connectionType()
	if err != nil {
		return err
	}
	c.Type = typ

	if err := c.SSH.resolve(strings.TrimSpace(c.Name), &c.Host); err != nil {
		return err
	}
	if c.Name == "" {
		if c.SSH.parsedUser != "" {
			c.Name = fmt.Sprintf("%s@%s:%d", c.SSH.parsedUser, c.SSH.parsedHost, c.SSH.parsedPort)
		} else {
			c.Name = fmt.Sprintf("%s:%d", c.SSH.parsedHost, c.SSH.parsedPort)
		}
	}
	return c.SSH.validate(c.Name)
}

// resolve determines the host, port and user from the connection's host, which
// it sets to the connection's name if there is none.
func (c *SSHConfig) resolve(name string, hostField *string) error {
	trimmedHost := strings.TrimSpace(*hostField)
	if trimmedHost == "" {
		if name == "" {
			return fmt.Errorf("either host or name must not be empty")
		}
		trimmedHost = name
		*hostField = name
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
	return nil
}

// validate sets defaults, and checks the tmux and port forward blocks of the
// connection called name.
func (c *SSHConfig) validate(name string) error {
	if err := c.Tmux.Validate(); err != nil {
		return err
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = pssh.DefaultDialTimeout
	}

	localPorts := make(map[int]bool)
	for i := range c.PortForward.Forwards {
		if err := c.PortForward.Forwards[i].Validate(); err != nil {
			return fmt.Errorf("connection %q forward #%d: %w", name, i+1, err)
		}
		lp := c.PortForward.Forwards[i].LocalPort
		if localPorts[lp] {
			return fmt.Errorf("connection %q: duplicate local forward port %d", name, lp)
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
	Connections []ConnectionConfig    `yaml:"connections" doc:"persistent ssh connections, each of which may have port forwards, for 'pconn connect', and a tmux session, for 'pconn tmux'"`
}

// Groups returns a sorted list of all unique group names across all connections.
func (cfg *Config) Groups() []string {
	seen := make(map[string]bool)
	var groups []string
	for _, c := range cfg.Connections {
		for _, g := range c.SSH.PortForward.Groups {
			if !seen[g] {
				seen[g] = true
				groups = append(groups, g)
			}
		}
	}
	slices.Sort(groups)
	return groups
}

// ProxyHostOnly reports whether name is a connection that exists only to be
// proxyed through, and so is maintained with the connections that are reached
// through it rather than being selected in its own right.
func (cfg *Config) ProxyHostOnly(name string) bool {
	for _, c := range cfg.Connections {
		if c.Name == name {
			return c.usedAsProxy && !c.MaintainedByConnect()
		}
	}
	return false
}

// ConnectConnections returns the connections that 'pconn connect' maintains.
func (cfg *Config) ConnectConnections() []ConnectionConfig {
	var result []ConnectionConfig
	for _, c := range cfg.Connections {
		if c.MaintainedByConnect() {
			result = append(result, c)
		}
	}
	return result
}

// Connection returns the named connection.
func (cfg *Config) Connection(name string) (ConnectionConfig, error) {
	var names []string
	for _, c := range cfg.Connections {
		if c.Name == name {
			return c, nil
		}
		names = append(names, c.Name)
	}
	return ConnectionConfig{}, fmt.Errorf("connection %q not found (available: %s)", name, strings.Join(names, ", "))
}

// ConnectionsForGroup returns all connections that belong to the specified group.
// The special group name "all" returns all configured connections.
func (cfg *Config) ConnectionsForGroup(group string) ([]ConnectionConfig, error) {
	if group == "all" {
		all := cfg.ConnectConnections()
		if len(all) == 0 {
			return nil, fmt.Errorf("no connections to maintain defined in configuration")
		}
		return all, nil
	}
	var matched []ConnectionConfig
	for _, c := range cfg.Connections {
		if c.MaintainedByConnect() && c.InGroup(group) {
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
	for _, c := range cfg.ConnectConnections() {
		if len(c.SSH.PortForward.Groups) == 0 {
			result = append(result, c)
		}
	}
	return result
}

// HasConflictingPorts reports whether any two connections in conns share a local port forward.
func HasConflictingPorts(conns []ConnectionConfig) (int, string, string, bool) {
	ports := make(map[int]string)
	for _, c := range conns {
		for _, f := range c.SSH.PortForward.Forwards {
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

// looksLikeHost reports whether spec names a host rather than a connection. A
// bare word that names no connection is far more likely to be a mistyped name
// than a hostname, and is reported as one.
func looksLikeHost(spec string) bool {
	return strings.ContainsAny(spec, "@:.")
}

// inlineProxyHost returns the connection for a proxy host given as a host rather
// than by name. It has nowhere of its own to say how it is reached, so it is
// reached as the connection that names it is.
func inlineProxyHost(spec string, from *ConnectionConfig) (*ConnectionConfig, error) {
	hop := &ConnectionConfig{
		Name: spec,
		Type: "ssh",
		Host: spec,
		SSH: &SSHConfig{
			DialTimeout:       from.SSH.DialTimeout,
			AcceptNewHostKeys: from.SSH.AcceptNewHostKeys,
			KnownHosts:        slices.Clone(from.SSH.KnownHosts),
		},
	}
	if err := hop.Validate(); err != nil {
		return nil, err
	}
	return hop, nil
}

// resolveProxyHosts resolves the proxy host of each connection that names one,
// which is another connection by name or, for a host that has no connection of
// its own, the host itself. A proxy host must be reachable directly, so one that
// itself has a proxy is an error rather than the start of a chain.
func (cfg *Config) resolveProxyHosts() error {
	byName := make(map[string]*ConnectionConfig, len(cfg.Connections))
	for i := range cfg.Connections {
		byName[cfg.Connections[i].Name] = &cfg.Connections[i]
	}
	for i := range cfg.Connections {
		c := &cfg.Connections[i]
		spec := strings.TrimSpace(c.SSH.Proxy)
		if spec == "" {
			continue
		}
		if spec == c.Name {
			return fmt.Errorf("connection %q: proxy %q is the connection itself", c.Name, spec)
		}
		hop, byRef := byName[spec]
		switch {
		case byRef:
			// The hop is maintained for as long as the connections that are
			// reached through it are, see MaintainedByConnect.
			hop.usedAsProxy = true
		case !looksLikeHost(spec):
			var names []string
			for _, other := range cfg.Connections {
				names = append(names, other.Name)
			}
			return fmt.Errorf("connection %q: proxy %q: no connection of that name (available: %s); a proxy host that has no connection of its own must be given as host, user@host or user@host:port",
				c.Name, spec, strings.Join(names, ", "))
		default:
			inline, err := inlineProxyHost(spec, c)
			if err != nil {
				return fmt.Errorf("connection %q: proxy %q: %w", c.Name, spec, err)
			}
			hop = inline
		}
		if proxy := strings.TrimSpace(hop.SSH.Proxy); proxy != "" {
			return fmt.Errorf("connection %q: proxy host %q itself has a proxy host (%q): only one hop is supported",
				c.Name, hop.Name, proxy)
		}
		c.proxy = hop
	}
	return nil
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

	// After the connections themselves, so that a proxy host named by one is
	// resolved to a connection that has been validated and had its defaults
	// set, and one given inline can inherit them.
	if err := cfg.resolveProxyHosts(); err != nil {
		return err
	}

	groups := cfg.Groups()
	if len(groups) == 0 {
		allLocalPorts := make(map[int]string)
		for _, c := range cfg.Connections {
			for _, f := range c.SSH.PortForward.Forwards {
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
				for _, f := range c.SSH.PortForward.Forwards {
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
