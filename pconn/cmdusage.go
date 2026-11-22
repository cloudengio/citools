// Usage of pconn
//
//	manage persistent network connections
//
//	connect - create and maintain persistent connections for one or more groups or all configured connections
//	   tmux - attach to a tmux session on a connection, reconnecting and reattaching if it is lost
//
// global flags: [--config=config.yaml --log-file= --log-format=json --log-level=0 --log-source-code=false --log-tempfile= --verbose=false]
//
//	-config value
//	  YAML configuration file; if unset, config.yaml in the pconn directory
//	  of the user configuration directories is used. The current directory is
//	  not searched unless a file is named here (default config.yaml)
//	-log-file string
//	  log file path. If not specified logs are written to stderr, if set to -
//	  logs are written to stdout
//	-log-format string
//	  log format: text or json (default "json")
//	-log-level int
//	  logging level: 0=error, 1=warn, 2=info, 3=debug
//	-log-source-code
//	  include source code file and line number in logs
//	-log-tempfile string
//	  create a unique log file from this mktemp pattern, eg. myapp-XXXXXX.log,
//	  in the temporary directory unless the pattern includes a directory, which
//	  is created if needed. Mutually exclusive with log-file
//	-verbose
//	  enable verbose logging
package main
