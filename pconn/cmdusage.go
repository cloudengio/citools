// Usage of pconn
//
//	manage persistent network connections
//
//	connect - create and maintain persistent connections for one or more groups or all configured connections
//
// global flags: [--config=config.yaml --log-file= --log-format=json --log-level=0 --log-source-code=false --verbose=false]
//
//	-config string
//	  path to YAML configuration file (default "config.yaml")
//	-log-file string
//	  log file path. If not specified logs are written to stderr, if set to -
//	  logs are written to stdout
//	-log-format string
//	  log format: text or json (default "json")
//	-log-level int
//	  logging level: 0=error, 1=warn, 2=info, 3=debug
//	-log-source-code
//	  include source code file and line number in logs
//	-verbose
//	  enable verbose logging
package main
