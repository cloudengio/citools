# citools

A collection of tools and utilities for CI/CD workflows, multi-module Go repository management, self-hosted runner orchestration, vulnerability management, and automated testing setup.

## Tools & Packages

| Tool / Package | Description |
| --- | --- |
| [multimod](multimod) | Run commands and maintenance actions (build, test, lint, tidy, annotate) across multiple Go modules in a monorepo. |
| [astest](astest) | Code generator that emits `*testing.T` wrappers for functions accepting a `TestingT` interface marked with `//cicd:astest`. |
| [chrome-for-testing](chrome-for-testing) | CLI tool and library for fetching, downloading, installing, and managing Chrome for Testing binaries across Linux, macOS, and Windows. |
| [github-runners](github-runners) | Orchestrator, web UI, and Packer VM image definitions (Linux & macOS) for managing GitHub self-hosted runner infrastructure. |
| [github-orchestrator](github-orchestrator) | Utility for installing and running the GitHub self-hosted runner orchestrator as a system service. |
| [govulnchecker](govulnchecker) | Wrapper around `govulncheck` allowing specific vulnerability IDs to be ignored using a YAML configuration file. |
| [waitforfiles](waitforfiles) | CLI utility that polls for the creation of one or more files with configurable timeouts and intervals. |

## Subdirectory Overview

- **[multimod](multimod)**: Scans a repository for `go.mod` files and executes defined actions across modules sequentially or concurrently.
- **[astest](astest)**: Scans packages for `//cicd:astest` annotations and generates runnable `*testing.T` wrapper test cases.
- **[chrome-for-testing](chrome-for-testing)**: Automates fetching release manifests and installing specific versions of Chrome for Testing binaries and user data directories.
- **[github-runners](github-runners)**: Contains:
  - `orchestrator`: Service and web API/UI backend to automate dynamic GitHub runner lifecycle management.
  - `linux`: Packer configuration (`build.pkr.hcl`) and scripts for building Linux runner VM images.
  - `macos`: Packer configuration (`build.pkr.hcl`) and scripts for building macOS runner VM images.
- **[github-orchestrator](github-orchestrator)**: Service installer for running the runner orchestrator daemon as a system service.
- **[govulnchecker](govulnchecker)**: Reads `.govulnchecker.yaml` to filter known or false-positive OSV vulnerability reports from `govulncheck`.
- **[waitforfiles](waitforfiles)**: Blocks until targeted files appear on disk or a timeout is reached. Useful in asynchronous pipeline steps.

## Development

The repository uses Go Workspaces (`go.work`) and `multimod` to manage multi-module builds.

Common `Makefile` targets:

```bash
make build  # Build all submodules via multimod
make test   # Run tests across all submodules
make lint   # Run linters across all submodules
make deps   # Sync dependencies and tidy all submodules
```

