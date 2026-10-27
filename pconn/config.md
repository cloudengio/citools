# Configuration

`pconn` is configured using a YAML configuration file. The configuration defines logging settings, persistent connections, their protocol types, group memberships, and local TCP port forwards to maintain.

By default the file is `config.yaml` in `pconn`'s own configuration directory: `~/Library/Application Support/pconn` on macOS, `$XDG_CONFIG_HOME/pconn` or `~/.config/pconn` elsewhere. The current directory is deliberately not searched, since `pconn` is run from wherever its connections are wanted and a `config.yaml` there belongs to whatever is being worked on rather than to `pconn`. Use `--config <file>` for a file anywhere else, including one in the current directory.

## Example Configuration

```yaml
logging:
  level: 1 # 0=error, 1=warn, 2=info, 3=debug
  format: text # text or json

connections:
  - name: dev-web
    type: ssh # connection type; defaults to ssh if omitted
    host: web.dev.example.com # if omitted, defaults to name
    port: 22
    user: admin
    dial_timeout: 30s
    groups: [dev, frontend]
    forwards: [8080:80, 8443:443]

  - name: db.dev.example.com:2222 # host is omitted, so name is used as the host
    type: ssh
    dial_timeout: 15s
    groups: [dev, backend]
    forwards: [5432:5432]

  - name: shared-cache
    type: ssh
    host: redis.internal.example.com
    groups: [backend, staging]
    forwards: [6379:6379]
```

## Configuration Specification

### Top-Level Fields

- `logging` *(optional)*: Configures logging level and format.
  - `level`: Integer log level (`0` = error, `1` = warn, `2` = info, `3` = debug).
  - `format`: Output format, either `text` or `json`.
  - `file`: Path to write log output (default writes to stderr; `-` writes to stdout).
- `connections`: List of connection definitions. At least one connection must be specified.

### Connection Fields

Each entry under `connections` supports the following settings:

- `name` *(optional)*: A unique identifier for the connection. If omitted, defaults to `user@host:port` (or `host:port` if user is unspecified).
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
- `unlimited_retries` *(optional)*: Boolean flag specifying whether reconnection attempts should back off and retry indefinitely. If unspecified, inherits the command-line flag setting (default `true`).
- `groups` *(optional)*: List of group names that this connection belongs to, typically specified using flow syntax (e.g. `[ci]` or `[dev, frontend]`). A connection may belong to multiple groups simultaneously.
- `forwards` *(optional)*: List of local TCP port forwarding tunnels to establish and maintain over this connection, specified using flow syntax (e.g. `[11001:11000]` or `[8080:80, 8443:443]`).

### Port Forwarding Fields

Each item under `forwards` can be specified as a `<local>:<remote>` string or mapping:

- **String syntax**: `<local>:<remote>` (e.g. `11001:11000`, `8080:80`).
- **Mapping syntax** *(backwards-compatible)*:
  - `local_port` (or alias `local`) *(required)*: Local TCP port on `127.0.0.1` to listen on (must be between 1 and 65535).
  - `remote_port` (or alias `remote`) *(required)*: Remote TCP port on the target host (or reachable from the remote host) to forward incoming connections to (must be between 1 and 65535).

## Groups and Membership

Connections can belong to multiple logical groups via the `groups` slice:

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
