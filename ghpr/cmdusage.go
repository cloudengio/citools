// Usage of ghpr
//
//	create and maintain github pull request descriptions with an ai agent
//
//	     create-pr - create a pull request for the current branch, with a description generated from its diff
//	update-summary - regenerate the description of a pull request from what its branch now changes
//	     post-note - attach the description of a merged pull request to the commit it created on the base branch
//	        config - config related commands
//
// global flags: [--base=main --config= --dry-run=false --engine=claude --max-diff-lines=3000 --model= --verbose=false --yes=false]
//
//	-base string
//	  branch that pull requests are merged into (default "main")
//	-config string
//	  path to YAML configuration file; if unset, config.yaml or ghpr.yaml is
//	  searched in standard locations
//	-dry-run
//	  report what would be changed without changing it
//	-engine string
//	  agent used to generate descriptions: claude or agy (default "claude")
//	-max-diff-lines int
//	  summarize the diff stat instead of the diff once it is longer than this;
//	  0 for no limit (default 3000)
//	-model string
//	  model for the agent to use; the agent's own default if unset
//	-verbose
//	  explain what is being done
//	-yes
//	  answer yes to every confirmation
package main
