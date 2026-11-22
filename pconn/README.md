# [cloudeng.io/citools/pconn](https://pkg.go.dev/cloudeng.io/citools/pconn?tab=doc)


Usage of `pconn`

    manage persistent network connections

    connect - maintain the port forwards in the port_forwards section of the configuration for one or more groups, or for all of them
       tmux - attach to a tmux session on a host in the tmux section of the configuration, reconnecting and reattaching if the connection is lost
     config - show the configuration, and its documentation.

global flags: [--config=config.yaml --log-file= --log-format=json
--log-level=0 --log-source-code=false --log-tempfile= --verbose=false]

    -config value
      YAML configuration file; if unset, config.yaml in the `pconn` directory
      of the user configuration directories is used. The current directory is
      not searched unless a file is named here (default config.yaml)
    -log-file string
      log file path. If not specified logs are written to stderr, if set to -
      logs are written to stdout
    -log-format string
      log format: text or json (default "json")
    -log-level int
      logging level: 0=error, 1=warn, 2=info, 3=debug
    -log-source-code
      include source code file and line number in logs
    -log-tempfile string
      create a unique log file from this mktemp pattern, eg. myapp-XXXXXX.log,
      in the temporary directory unless the pattern includes a directory, which
      is created if needed. Mutually exclusive with log-file
    -verbose
      enable verbose logging

`pconn` manages a set of persistent network connections (such as SSH) and port
forwards.

It establishes and maintains persistent connections based on a YAML
configuration file, automatically reconnecting dropped connections using
exponential backoff. The configuration has two sections: port_forwards,
whose entries can belong to multiple groups, allowing sets of related
connections to be started and maintained together by the connect command,
and tmux, whose entries are used by the tmux command. The config command
shows and documents the configuration.

The tmux command attaches to a tmux session over one of the connections
in the tmux section of the configuration, running the command line that
its command field configures, eg. 'tmux -CC new -A -s main' to create
or attach to a session using the control mode supported by iTerm2.
If the connection is lost it is reestablished and the session reattached,
which leaves the session itself, and what is running in it, undisturbed.
Logs are written to a temporary file unless a log file is configured,
since anything else written to the terminal would corrupt the session.


## External Markdown Files Included Here

### Configuration (config.md)

`pconn` is configured using a YAML configuration file. The configuration defines logging settings and two sections: `port_forwards`, which holds the persistent connections that `pconn connect` maintains and the local TCP port forwards they carry, and `tmux`, which holds the connections that `pconn tmux` runs a tmux session over. `pconn config describe` documents the fields, and `pconn config show` shows the configuration that was read, and the file it came from.

By default the file is `config.yaml` in `pconn`'s own configuration directory: `~/Library/Application Support/pconn` on macOS, `$XDG_CONFIG_HOME/pconn` or `~/.config/pconn` elsewhere. The current directory is deliberately not searched, since `pconn` is run from wherever its connections are wanted and a `config.yaml` there belongs to whatever is being worked on rather than to `pconn`. Use `--config <file>` for a file anywhere else, including one in the current directory.

#### Example Configuration

```yaml
logging:
  level: 1 # 0=error, 1=warn, 2=info, 3=debug
  format: text # text or json

# Hosts whose ports are forwarded by 'pconn connect'.
port_forwards:
  # A fully specified entry: nothing is left to default.
  - name: dev-web
    type: ssh # connection type; defaults to ssh if omitted
    host: web.dev.example.com
    port: 22
    user: admin
    dial_timeout: 30s
    accept_new_host_keys: true # accept and record host keys not yet known
    forward_agent: false # make the local ssh agent available to commands run on the host
    groups: [dev, frontend]
    forwards: [8080:80, 8443:443] # <local>:<remote>

  # host is omitted, so the name is used as the host; it carries the port.
  - name: db.dev.example.com:2222
    user: dba
    dial_timeout: 15s
    known_hosts: [/etc/ssh/ssh_known_hosts]
    unlimited_retries: false # give up once the retries are exhausted
    groups: [dev, backend]
    forwards:
      # A forward may also be written as a mapping. local/remote are accepted
      # as aliases for local_port/remote_port.
      - local_port: 5432
        remote_port: 5432

  # A minimal entry: type, port, user and dial_timeout all default.
  - name: shared-cache
    host: redis.internal.example.com
    groups: [backend, staging]
    forwards: [6379:6379]

# Hosts that 'pconn tmux <name>' runs a tmux session on. The same host may also
# appear in port_forwards; the sections are independent.
tmux:
  - name: dev-web
    host: web.dev.example.com
    user: admin
    forward_agent: true # only for hosts that are trusted
    # The complete command line, run as written by the remote login shell. The
    # login shell does not read .zshrc or .bashrc, so give the full path to
    # tmux if it is only on the PATH set there.
    command: /opt/homebrew/bin/tmux -u -CC new -A -s main
```

#### Configuration Specification

##### Top-Level Fields

- `logging` *(optional)*: Configures logging level and format.
  - `level`: Integer log level (`0` = error, `1` = warn, `2` = info, `3` = debug).
  - `format`: Output format, either `text` or `json`.
  - `file`: Path to write log output (default writes to stderr; `-` writes to stdout).
- `port_forwards` *(optional)*: List of connections that carry port forwards, used by `pconn connect`, see [Port Forward Fields](#port-forward-fields).
- `tmux` *(optional)*: List of connections over which `pconn tmux` runs a tmux session, see [tmux Fields](#tmux-fields).

At least one entry must be specified in one of the two sections. The sections are independent: names need only be unique within a section, and the same host may appear in both. Unknown fields are an error, so a configuration that uses the earlier `connections` section is rejected rather than ignored.

##### Connection Fields

Every entry in `port_forwards` and in `tmux` supports the following settings, which say how to reach the host:

- `name` *(optional)*: A unique identifier for the entry. If omitted, defaults to `user@host:port` (or `host:port` if user is unspecified).
- `type` *(optional)*: The connection type or protocol to use. Defaults to `ssh`. Currently, `ssh` is the supported implementation, serving as an extensible foundation for other connection protocols in the future.
- `host` *(optional)*: The target host. If omitted, `name` is used as the host. When specified, can be formatted as:
  - Bare hostname or IP: `server.example.com`
  - Host and port: `server.example.com:2222`
  - User and host: `admin@server.example.com`
  - User, host, and port: `admin@server.example.com:2222`
  - Bracketed IPv6: `[::1]:2222` or `user@[fe80::1]:22`
- `port` / `ssh_port` *(optional)*: Remote port. Defaults to `22` if not explicitly specified in `host` or `name`.
- `user` *(optional)*: Username to authenticate as. If omitted, defaults to the user in `host`/`name` or the current environment user (`$USER`).
- `dial_timeout` *(optional)*: Duration to wait when establishing the connection (e.g. `10s`, `30s`, `1m`). Defaults to `30s`.
- `accept_new_host_keys` *(optional)*: When true (for SSH), automatically trusts and records newly encountered SSH host public keys.
- `known_hosts` *(optional)*: List of custom `known_hosts` file paths to consult during SSH host verification.
- `forward_agent` *(optional)*: When true, the local ssh agent (`$SSH_AUTH_SOCK`) is made available to commands run on the remote host, as `ssh -A` does; sessions started by them then have an `SSH_AUTH_SOCK` of their own. It is accepted in both sections, but `pconn connect` only maintains the connection and port forwards and runs no commands, so it only has an effect for `tmux` entries. Anyone able to act as you, or as root, on the remote host can use your agent whilst a command runs, so enable it only for hosts that are trusted. Defaults to `false`. If the host refuses forwarding the command is run without it and a warning is logged.

##### Port Forward Fields

In addition to the connection fields, each entry under `port_forwards` supports:

- `forwards` *(optional)*: List of local TCP port forwarding tunnels to establish and maintain over this connection, specified using flow syntax (e.g. `[11001:11000]` or `[8080:80, 8443:443]`), see [Port Forwarding Fields](#port-forwarding-fields).
- `groups` *(optional)*: List of group names that this entry belongs to, typically specified using flow syntax (e.g. `[ci]` or `[dev, frontend]`). An entry may belong to multiple groups simultaneously. Groups only apply to `port_forwards`.
- `unlimited_retries` *(optional)*: Boolean flag specifying whether reconnection attempts should back off and retry indefinitely. If unspecified, inherits the command-line flag setting (default `true`).

##### tmux Fields

In addition to the connection fields, each entry under `tmux` supports:

- `command` *(required by `pconn tmux`)*: The complete command line to run on the remote host, for example `/opt/homebrew/bin/tmux -u -CC new -A -s main`, which creates the session `main` or attaches to it. It is passed to the user's login shell as written, so it is subject to that shell's quoting, and that shell does not read interactive-only startup files such as `.zshrc`, so give the full path to tmux if it is only on the `PATH` set there. There is no default. The `--command` flag overrides it.

`pconn tmux <name>` uses the entry in `tmux` with that name:

```yaml
tmux:
  - name: dev-web
    host: web.dev.example.com
    forward_agent: true
    command: /opt/homebrew/bin/tmux -u -CC -L work new -A -s main
```

##### Port Forwarding Fields

Each item under `forwards` can be specified as a `<local>:<remote>` string or mapping:

- **String syntax**: `<local>:<remote>` (e.g. `11001:11000`, `8080:80`).
- **Mapping syntax** *(backwards-compatible)*:
  - `local_port` (or alias `local`) *(required)*: Local TCP port on `127.0.0.1` to listen on (must be between 1 and 65535).
  - `remote_port` (or alias `remote`) *(required)*: Remote TCP port on the target host (or reachable from the remote host) to forward incoming connections to (must be between 1 and 65535).

#### Groups and Membership

Entries in `port_forwards` can belong to multiple logical groups via the `groups` slice:

- **Multi-Group Membership**: A connection can serve multiple roles. For example, a database proxy can belong to both `dev` and `backend` groups: `groups: [dev, backend]`.
- **Scanning and Filtering**: When `pconn connect <group>...` is invoked with one or more group names, the configuration is scanned and all connections whose `groups` list contains any of the specified groups are deduplicated, selected, and maintained together.
- **The `all` Group**: Specifying `all` (e.g. `pconn connect all`) selects all connections defined in the configuration file.
- **Single Connection Selection**: If an argument given to `connect` does not match any group name, `pconn` checks if it matches a specific connection `name` and includes that connection.
- **Default Behavior**: If no group is specified (`pconn connect`), all connections defined in the configuration are maintained, provided there are no local port collisions across different groups.

#### Port Conflict Resolution

- **Within a Connection**: A single connection cannot declare multiple forwards binding the same local port.
- **Within a Group**: Multiple connections belonging to the same group cannot bind the same local port. Conflicting configurations are caught during startup validation.
- **Across Disjoint Groups**: Connections belonging to different groups may reuse the same local port (for instance, local port `5432` pointing to a dev database in the `dev` group and to a staging database in the `staging` group). Because only one group is active at a time, no collision occurs.
- If `pconn connect` is run without specifying a group when disjoint groups reuse a local port, `pconn` detects the potential conflict and exits with an error indicating which connections conflict and listing the available groups.


