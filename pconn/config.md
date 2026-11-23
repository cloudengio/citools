# Configuration

`pconn` is configured using a YAML configuration file. The configuration defines logging settings and persistent connections. Each connection has a `name` and exactly one block of fields specific to its type, which that block implies. Only `ssh` is supported at present: its `ssh` block says how to reach a host, and has two optional blocks that say what is done over it, `port_forward`, which `pconn connect` maintains, and `tmux`, which `pconn tmux` runs. `pconn config describe` documents the fields, and `pconn config show` shows the configuration that was read, and the file it came from. Unknown fields are an error.

By default the file is `config.yaml` in `pconn`'s own configuration directory: `~/Library/Application Support/pconn` on macOS, `$XDG_CONFIG_HOME/pconn` or `~/.config/pconn` elsewhere. The current directory is deliberately not searched, since `pconn` is run from wherever its connections are wanted and a `config.yaml` there belongs to whatever is being worked on rather than to `pconn`. Use `--config <file>` for a file anywhere else, including one in the current directory.

## Example Configuration

```yaml
logging:
  level: 1 # 0=error, 1=warn, 2=info, 3=debug
  format: text # text or json

# Each connection has a name and exactly one block of fields specific to its
# type, which is implied by the block. Only ssh is supported at present. For
# ssh, the port_forward block is used by 'pconn connect', and the tmux block by
# 'pconn tmux <name>'.
connections:
  # A fully specified connection: nothing is left to default.
  - name: dev-web
    type: ssh # optional, since the ssh block implies it
    host: web.dev.example.com
    ssh:
      port: 22
      user: admin
      dial_timeout: 30s
      accept_new_host_keys: true # accept and record host keys not yet known
      port_forward:
        groups: [dev, frontend]
        forwards: [8080:80, 8443:443] # <local>:<remote>
      # tmux is run over the same host, using its own connection.
      tmux:
        forward_agent: true # only for hosts that are trusted
        # The complete command line, run as written by the remote login shell.
        # The login shell does not read .zshrc or .bashrc, so give the full
        # path to tmux if it is only on the PATH set there.
        command: /opt/homebrew/bin/tmux -u -CC new -A -s main

  # host is omitted, so the name is used as the host; it carries the port.
  - name: db.dev.example.com:2222
    ssh:
      user: dba
      dial_timeout: 15s
      known_hosts: [/etc/ssh/ssh_known_hosts]
      port_forward:
        unlimited_retries: false # give up once the retries are exhausted
        groups: [dev, backend]
        forwards:
          # A forward may also be written as a mapping. local/remote are
          # accepted as aliases for local_port/remote_port.
          - local_port: 5432
            remote_port: 5432

  # A minimal connection: port, user and dial_timeout all default.
  - name: shared-cache
    host: redis.internal.example.com
    ssh:
      port_forward:
        groups: [backend, staging]
        forwards: [6379:6379]

  # A connection that only has a tmux block is not maintained by 'pconn connect'.
  - name: build-box
    host: build.example.com
    ssh:
      tmux:
        command: /usr/local/bin/tmux -CC new -A -s main

  # A bastion, which carries the connection below. It is a connection of its
  # own so that the fields saying how to reach it are stated once however many
  # connections are reached through it, and they share one connection to it.
  # Having no port forwards and no tmux of its own, it is maintained for as
  # long as they are rather than being one to connect to in its own right.
  - name: bastion
    host: bastion.example.com
    ssh:
      user: admin
      accept_new_host_keys: true

  # A host that is not reachable from here, so it is reached through the
  # bastion, as ssh's ProxyJump ('ssh -J') does.
  - name: metrics
    host: metrics.internal
    ssh:
      proxy: bastion
      port_forward:
        groups: [backend]
        forwards: [9090:9090]
```

## Configuration Specification

### Top-Level Fields

- `logging` *(optional)*: Configures logging level and format.
  - `level`: Integer log level (`0` = error, `1` = warn, `2` = info, `3` = debug).
  - `format`: Output format, either `text` or `json`.
  - `file`: Path to write log output (default writes to stderr; `-` writes to stdout).
- `connections`: List of connection definitions. At least one connection must be specified.

### Connection Fields

Each entry under `connections` supports the following settings, which are common to all types of connection:

- `name` *(optional)*: A unique identifier for the connection. If omitted, defaults to `user@host:port` (or `host:port` if user is unspecified).
- `type` *(optional)*: The connection type, which is implied by the type specific block that is set, so it can be left out. If it is given it must name that block, and if the block itself is not needed, `type: ssh` alone is enough to select it. `ssh` is the only type at present.
- `host` *(optional)*: The remote host, as a hostname or an IPv4 or IPv6 address, for example `server.example.com`, `192.0.2.1` or `2001:db8::1`. If omitted, `name` is used as the host. For `ssh` it may also carry a user and a port:
  - Host and port: `server.example.com:2222` or `192.0.2.1:2222`
  - User and host: `admin@server.example.com`
  - User, host, and port: `admin@server.example.com:2222`
  - Bracketed IPv6, which is needed to give a port: `[2001:db8::1]:2222` or `admin@[fe80::1]:22`
- `ssh`: The fields for a connection of type `ssh`, see [ssh Fields](#ssh-fields). Exactly one type specific block must be set, so that a connection of another type, added later as a block of its own alongside `ssh`, has no `ssh` block.

### ssh Fields

The `ssh` block says how to connect to the host, and what is done over it:

- `port` / `ssh_port` *(optional)*: Remote port. Defaults to `22` if not explicitly specified in `host` or `name`.
- `user` *(optional)*: Username to authenticate as. If omitted, defaults to the user in `host`/`name` or the current environment user (`$USER`).
- `dial_timeout` *(optional)*: Duration to wait when establishing the connection (e.g. `10s`, `30s`, `1m`). Defaults to `30s`.
- `accept_new_host_keys` *(optional)*: When true, automatically trusts and records newly encountered SSH host public keys.
- `known_hosts` *(optional)*: List of custom `known_hosts` file paths to consult during SSH host verification.
- `proxy` *(optional)*: Another host to reach this one through, as ssh's `ProxyJump` (`ssh -J`) does, for example a host behind a bastion, see [Proxy Hosts](#proxy-hosts).
- `port_forward` *(optional)*: The port forwards that `pconn connect` maintains, see [ssh port_forward Fields](#ssh-port_forward-fields).
- `tmux` *(optional)*: How `pconn tmux` runs tmux, see [ssh tmux Fields](#ssh-tmux-fields).

`pconn connect` maintains every connection that has a `port_forward` block, and every connection that has neither block, except one that exists only to be proxyed through, see [Proxy Hosts](#proxy-hosts). A connection that only has a `tmux` block is left to `pconn tmux`. Each of the two commands makes its own connection, so the blocks of one connection do not share one.

### ssh port_forward Fields

- `forwards` *(optional)*: List of local TCP port forwarding tunnels to establish and maintain over this connection, specified using flow syntax (e.g. `[11001:11000]` or `[8080:80, 8443:443]`), see [Port Forwarding Fields](#port-forwarding-fields).
- `groups` *(optional)*: List of group names that this connection belongs to, typically specified using flow syntax (e.g. `[ci]` or `[dev, frontend]`). A connection may belong to multiple groups simultaneously. Groups only apply to port forwards.
- `unlimited_retries` *(optional)*: Boolean flag specifying whether reconnection attempts should back off and retry indefinitely. If unspecified, inherits the command-line flag setting (default `true`).
- `forward_agent` *(optional)*: As for `tmux`, but `pconn connect` runs no commands on the host, so there is nothing to make the agent available to and it has no practical effect.

### ssh tmux Fields

- `command` *(required by `pconn tmux`)*: The complete command line to run on the remote host, for example `/opt/homebrew/bin/tmux -u -CC new -A -s main`, which creates the session `main` or attaches to it. It is passed to the user's login shell as written, so it is subject to that shell's quoting, and that shell does not read interactive-only startup files such as `.zshrc`, so give the full path to tmux if it is only on the `PATH` set there. There is no default. The `--command` flag overrides it.
- `forward_agent` *(optional)*: When true, the local ssh agent (`$SSH_AUTH_SOCK`) is made available to tmux and the commands run within it, as `ssh -A` does; sessions started by it then have an `SSH_AUTH_SOCK` of their own. Anyone able to act as you, or as root, on the remote host can use your agent whilst a command runs, so enable it only for hosts that are trusted. Defaults to `false`. If the host refuses forwarding the command is run without it and a warning is logged.

```yaml
connections:
  - name: dev-web
    host: web.dev.example.com
    ssh:
      tmux:
        forward_agent: true
        command: /opt/homebrew/bin/tmux -u -CC -L work new -A -s main
```

### Proxy Hosts

A host that cannot be reached from where `pconn` runs is reached through one that can, which the `proxy` field of its `ssh` block names, as ssh's `ProxyJump` (`ssh -J`) does. The connection to it is carried by the connection to the proxy host: only the transport changes, so the host is still verified by its own host key under its own name, and its port forwards run over both connections.

`proxy` takes either of two forms:

- **The name of another connection**, for example `proxy: bastion`. That connection says how the proxy host is reached — its user, port, `known_hosts` and so on — so those fields are stated once however many connections are reached through it, and all of them share one connection to it rather than each making its own.
- **A host that has no connection of its own**, given as `host`, `user@host` or `user@host:port`, for example `proxy: admin@bastion.example.com:2222`. It has nowhere of its own to state the rest, so it takes its `dial_timeout`, `known_hosts` and `accept_new_host_keys` from the connection that names it. A value that is a bare word and names no connection is reported as a misspelt connection name rather than taken as a hostname.

A proxy host must itself be reachable directly: one that has a `proxy` of its own is an error rather than the start of a chain, as is a connection that names itself.

The two connections recover together. The connection to the host is carried by the proxy host's connection, so losing that loses this one too, and it is established again once the proxy host has reconnected; a proxy host that cannot be established at all ends the connections through it rather than leaving them retrying through one that will never come back. Both are reported by `pconn connect`, so a connection that is not yet established and a proxy host that is why are distinguishable, and the log line for each connection names the proxy host it goes through.

A connection that has neither `port_forward` nor `tmux` of its own and is used as a proxy host is maintained for as long as the connections reached through it are, rather than being one to connect to in its own right: it is not selected by `pconn connect all` or by group, and naming it says so. A proxy host that does have forwards of its own is maintained in its own right as well, over the same single connection.

### Port Forwarding Fields

Each item under `forwards` can be specified as a `<local>:<remote>` string or mapping:

- **String syntax**: `<local>:<remote>` (e.g. `11001:11000`, `8080:80`).
- **Mapping syntax** *(backwards-compatible)*:
  - `local_port` (or alias `local`) *(required)*: Local TCP port on `127.0.0.1` to listen on (must be between 1 and 65535).
  - `remote_port` (or alias `remote`) *(required)*: Remote TCP port on the target host (or reachable from the remote host) to forward incoming connections to (must be between 1 and 65535).

## Groups and Membership

Connections can belong to multiple logical groups via the `groups` slice of their `ssh` `port_forward` block:

- **Multi-Group Membership**: A connection can serve multiple roles. For example, a database proxy can belong to both `dev` and `backend` groups: `groups: [dev, backend]`.
- **Scanning and Filtering**: When `pconn connect <group>...` is invoked with one or more group names, the configuration is scanned and all connections whose `groups` list contains any of the specified groups are deduplicated, selected, and maintained together.
- **The `all` Group**: Specifying `all` (e.g. `pconn connect all`) selects all connections defined in the configuration file.
- **Single Connection Selection**: If an argument given to `connect` does not match any group name, `pconn` checks if it matches a specific connection `name` and includes that connection.
- **Default Behavior**: If no group is specified (`pconn connect`), all connections defined in the configuration are maintained, provided there are no local port collisions across different groups.

## Port Conflict Resolution

- **Within a Connection**: A single connection cannot declare multiple forwards binding the same local port.
- **Within a Group**: Multiple connections belonging to the same group cannot bind the same local port. Conflicting configurations are caught during startup validation.
- **Across Disjoint Groups**: Connections belonging to different groups may reuse the same local port (for instance, local port `5432` pointing to a dev database in the `dev` group and to a staging database in the `staging` group). Because only one group is active at a time, no collision occurs.
- If `pconn connect` is run without specifying a group when disjoint groups reuse a local port, `pconn` detects the potential conflict and exits with an error indicating which connections conflict and listing the available groups.
