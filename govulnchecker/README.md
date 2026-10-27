# [github.com/cloudengio/citools/govulnchecker](https://pkg.go.dev/github.com/cloudengio/citools/govulnchecker?tab=doc)


Usage of
/var/folders/6l/tm8174_x64j4xsrtfw4mbf380000gn/T/go-build2544859554/b001/exe/govulnchecker:

    -config string
      	Path to the YAML configuration file containing ignored vulnerabilities (default ".govulnchecker.yaml")
    -include-not-called
      	Include vulnerabilities that are imported but never called (symbol scan level only)

Command `govulnchecker` is a wrapper around the standard 'govulncheck' tool
that provides the ability to ignore specific known vulnerabilities.

It runs 'govulncheck -json' with any provided arguments and parses the
output stream, filtering out any vulnerabilities that match the IDs
specified in a YAML configuration file. If any non-ignored vulnerabilities
are found, the command exits with status 1.

The default configuration file is '.govulnchecker.yaml', which can be
overridden using the '-config' flag.

# Configuration File Format

The configuration file is a YAML document with an 'ignore' key containing a
list of vulnerabilities to ignore. Each item must have an 'id' (the OSV ID)
and typically a 'why' field providing the justification.

# Example

    ignore:
      - id: GO-2023-1234
        why: "We do not use the vulnerable function in this module."
      - id: GO-2022-5678
        why: "This is a false positive for our specific deployment environment."


