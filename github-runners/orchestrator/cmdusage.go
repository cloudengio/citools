// Usage of orchestrator
//
//	orchestrator for GitHub self-hosted runners
//
//	         run - run the orchestrator
//	     run-job - run a single job on a VM, useful for testing vms
//	      github - GitHub API commands
//	     install - write the bundled minimal orchestrator config file to a standard location
//	      bundle - build a signed macOS .app bundle that installs the orchestrator
//	     service - manage the orchestrator as a per-user launchd login service
//	webapp-build - build the embedded web UI frontend (runs npm install, gen and build)
//	      config - config related commands
//	         vms - inspect and clean up the VMs created by the orchestrator's pools
//
// global flags: [--config=github_orchestrator_config.yml --log-file= --log-format=json --log-level=0 --log-source-code=false --verbose=false]
//
//	-config string
//	  path to YAML configuration file (default "github_orchestrator_config.yml")
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
