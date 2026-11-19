// Usage of ghpr
//
//	create and maintain github pull request descriptions with an ai agent
//
//	              create-pr - create a pull request for the current branch, committing any uncommitted changes with the title first
//	         update-summary - regenerate the description of a pull request from what its branch now changes
//	     merge-with-summary - squash-merge a pull request with its description as the commit summary
//	              post-note - [deprecated] attach the description of a merged pull request to the commit it created on the base branch
//	 write-notes-as-commits - rewrite history from a commit onward to replace its commit body with its git note
//	list-commits-with-notes - list commits that have git notes, newest first, as candidates for write-notes-as-commits
//	                 config - show the configuration, and its documentation.
//
// global flags: [--base=main --config=config.yaml,ghpr.yaml --dry-run=false --engine=claude --max-diff-lines=3000 --model= --show-command=false --show-summary-command=false --verbose=false --yes=false]
//
//	-base string
//	  branch that pull requests are merged into (default "main")
//	-config value
//	  YAML configuration file; if unset, the first of config.yaml and ghpr.yaml
//	  found in the ghpr directory of the user configuration directories is
//	  used. The current directory is not searched unless a file is named here
//	  (default config.yaml,ghpr.yaml)
//	-dry-run
//	  report what would be changed without changing it
//	-engine string
//	  agent used to generate descriptions: claude or agy (default "claude")
//	-max-diff-lines int
//	  summarize the diff stat instead of the diff once it is longer than this;
//	  0 for no limit (default 3000)
//	-model string
//	  model for the agent to use; the agent's own default if unset
//	-show-command
//	  show the command that would be run to generate the summary
//	-show-summary-command
//	  show the command that would be run to generate the summary
//	-verbose
//	  explain what is being done
//	-yes
//	  answer yes to every confirmation
package main
