package main

import (
	"fmt"
	"io"
	"os"
)

// runCompletion prints a shell completion script. The scripts complete the
// subcommands and, for the id-taking commands, the infohash prefixes reported
// by `shoal status` and `shoal history` (the first table column).
//
//	shoal completion bash   >> ~/.bashrc          # or a bash-completion.d file
//	shoal completion zsh    > "${fpath[1]}/_shoal"
//	shoal completion fish   > ~/.config/fish/completions/shoal.fish
func runCompletion(args []string, out io.Writer) int {
	shell := ""
	if len(args) > 0 {
		shell = args[0]
	}
	var script string
	switch shell {
	case "bash":
		script = bashCompletion
	case "zsh":
		script = zshCompletion
	case "fish":
		script = fishCompletion
	default:
		fmt.Fprintln(os.Stderr, "usage: shoal completion bash|zsh|fish")
		return 2
	}
	fmt.Fprint(out, script)
	return 0
}

const bashCompletion = `# bash completion for shoal
_shoal() {
    local cur cmds ids
    cur="${COMP_WORDS[COMP_CWORD]}"
    cmds="sources search download status history pause resume remove open daemon skill update version help completion"
    if [ "$COMP_CWORD" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "$cmds" -- "$cur") )
        return
    fi
    case "${COMP_WORDS[1]}" in
        status|pause|resume|remove|open|download)
            ids=$( { shoal status 2>/dev/null | awk 'NR>1{print $1}'; shoal history 2>/dev/null | awk 'NR>1{print $1}'; } | sort -u )
            COMPREPLY=( $(compgen -W "$ids" -- "$cur") )
            ;;
        history)
            COMPREPLY=( $(compgen -W "rm clear" -- "$cur") )
            ;;
        sources)
            COMPREPLY=( $(compgen -W "enable disable" -- "$cur") )
            ;;
    esac
}
complete -o bashdefault -o default -F _shoal shoal
`

const zshCompletion = `#compdef shoal
_shoal() {
    local -a cmds ids
    cmds=(sources search download status history pause resume remove open daemon skill update version help completion)
    if (( CURRENT == 2 )); then
        compadd -- $cmds
        return
    fi
    case $words[2] in
        status|pause|resume|remove|open|download)
            ids=(${(f)"$( { shoal status 2>/dev/null | awk 'NR>1{print $1}'; shoal history 2>/dev/null | awk 'NR>1{print $1}'; } | sort -u )"})
            compadd -- $ids
            # download also accepts a path to a local .torrent file
            [[ $words[2] == download ]] && _files -g '*.torrent'
            ;;
        history) compadd -- rm clear ;;
        sources) compadd -- enable disable ;;
    esac
}
compdef _shoal shoal
`

const fishCompletion = `# fish completion for shoal
complete -c shoal -f -n __fish_use_subcommand -a 'sources search download status history pause resume remove open daemon skill update version help completion'
complete -c shoal -f -n '__fish_seen_subcommand_from status pause resume remove open' -a '(begin; shoal status 2>/dev/null | awk \'NR>1{print $1}\'; shoal history 2>/dev/null | awk \'NR>1{print $1}\'; end | sort -u)'
# download also accepts a local .torrent path, so leave file completion on (no -f)
complete -c shoal -n '__fish_seen_subcommand_from download' -a '(begin; shoal status 2>/dev/null | awk \'NR>1{print $1}\'; shoal history 2>/dev/null | awk \'NR>1{print $1}\'; end | sort -u)'
complete -c shoal -f -n '__fish_seen_subcommand_from history' -a 'rm clear'
complete -c shoal -f -n '__fish_seen_subcommand_from sources' -a 'enable disable'
`
