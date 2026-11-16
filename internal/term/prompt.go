package term

import (
	"strings"
)

// Campbell bright colours, matching the Windows Terminal Campbell palette.
// A coloured prompt uses the bright row (bold ANSI), which stays readable on #0c0c0c.
const (
	promptGreen = "22;198;12"  // #16C60C bright green
	promptBlue  = "59;120;255" // #3B78FF bright blue
	promptRed   = "231;72;86"  // #E74856 bright red
)

// applyTermEnv forces the terminal description programs look at for colour.
// TERM=xterm-256color is what GNU ls and grep treat as colour-capable.
// COLORTERM=truecolor advertises 24-bit colour to programs that check it.
func applyTermEnv(env []string) []string {
	out := make([]string, 0, len(env)+2)
	for _, e := range env {
		if strings.HasPrefix(e, "TERM=") || strings.HasPrefix(e, "COLORTERM=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, "TERM=xterm-256color", "COLORTERM=truecolor")
}

func stripEnv(env []string, keys ...string) []string {
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	out := make([]string, 0, len(env))
	for _, e := range env {
		k, _, ok := strings.Cut(e, "=")
		if ok && drop[k] {
			continue
		}
		out = append(out, e)
	}
	return out
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// promptLib is the bash used by both the host login shell and container consoles.
// It colours a stock prompt and leaves a prompt the user actually chose alone.
func promptLib() string {
	return `
feldcore_stock_ps1() {
  local p="$PS1"
  # Debian and Ubuntu prepend an xterm title sequence when TERM matches xterm*.
  if [[ "$p" == '\[\e]0;'* || "$p" == '\[\033]0;'* ]]; then
    p="${p#*\\a\\]}"
  fi
  case "$p" in
    ''|\
    '\s-\v\$ '|\
    '\s-\v\$'|\
    '\$ '|\
    '$ '|\
    '# '|\
    '\u@\h:\w\$ '|\
    '\u@\h:\w\$'|\
    '${debian_chroot:+($debian_chroot)}\u@\h:\w\$ '|\
    '${debian_chroot:+($debian_chroot)}\[\033[01;32m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ '|\
    '${debian_chroot:+($debian_chroot)}\[\033[01;31m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\# '|\
    '\[\033[01;32m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ '|\
    '\[\033[01;31m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\# ')
      return 0 ;;
  esac
  return 1
}
feldcore_install_prompt() {
  feldcore_stock_ps1 || return 0
  if [ "$(id -u)" -eq 0 ]; then
    PS1='\[\033[38;2;` + promptRed + `m\]\u@\h\[\033[0m\]:\[\033[38;2;` + promptBlue + `m\]\w\[\033[0m\]# '
  else
    PS1='\[\033[38;2;` + promptGreen + `m\]\u@\h\[\033[0m\]:\[\033[38;2;` + promptBlue + `m\]\w\[\033[0m\]\$ '
  fi
}
feldcore_color_aliases() {
  if [ -z "${LS_COLORS-}" ] && command -v dircolors >/dev/null 2>&1; then
    eval "$(dircolors -b 2>/dev/null)" || true
  fi
  alias ls >/dev/null 2>&1 || alias ls='ls --color=auto'
  alias grep >/dev/null 2>&1 || alias grep='grep --color=auto'
  alias fgrep >/dev/null 2>&1 || alias fgrep='fgrep --color=auto'
  alias egrep >/dev/null 2>&1 || alias egrep='egrep --color=auto'
}
`
}

// hostHook runs once, after bash -l has read the real profile and bashrc.
// A prompt the user set is kept. A stock prompt becomes Campbell green/blue,
// or Campbell red for root. ls and grep gain colour aliases when missing.
func hostHook() string {
	return "set +e\nexport TERM=xterm-256color\nexport COLORTERM=truecolor\n" + promptLib() + `
feldcore_install_prompt
feldcore_color_aliases
unset -f feldcore_stock_ps1 feldcore_install_prompt feldcore_color_aliases
if [ -n "${FELDCORE_PREV_PROMPT+x}" ]; then
  PROMPT_COMMAND=$FELDCORE_PREV_PROMPT
  unset FELDCORE_PREV_PROMPT
else
  unset PROMPT_COMMAND
fi
`
}

// containerInit is the bash --rcfile for a container console.
// Interactive non-login bash on Debian already sourced /etc/bash.bashrc;
// other builds still have the compiled-in prompt, so those source it here.
// ~/.bashrc is sourced because --rcfile replaces it.
func containerInit() string {
	return `set +e
export TERM=xterm-256color
export COLORTERM=truecolor
if [ "$PS1" = '\s-\v\$ ' ] || [ -z "${PS1+x}" ]; then
  if [ -f /etc/bash.bashrc ]; then
    . /etc/bash.bashrc
  elif [ -f /etc/bashrc ]; then
    . /etc/bashrc
  fi
fi
if [ -f "$HOME/.bashrc" ]; then
  . "$HOME/.bashrc"
fi
set +e
` + promptLib() + `
feldcore_install_prompt
feldcore_color_aliases
unset -f feldcore_stock_ps1 feldcore_install_prompt feldcore_color_aliases
`
}

// shBody colours a POSIX shell prompt when it is still the default.
const shBody = `
case ${PS1-} in
''|'$ '|'# '|'\$ ')
  _g=$(printf '\033[38;2;` + promptGreen + `m')
  _r=$(printf '\033[38;2;` + promptRed + `m')
  _b=$(printf '\033[38;2;` + promptBlue + `m')
  _o=$(printf '\033[0m')
  if [ "$(id -u)" -eq 0 ]; then
    PS1=$(printf '%s$(id -un)@$(hostname)%s:%s$PWD%s# ' "$_r" "$_o" "$_b" "$_o")
  else
    PS1=$(printf '%s$(id -un)@$(hostname)%s:%s$PWD%s$ ' "$_g" "$_o" "$_b" "$_o")
  fi
  unset _g _r _b _o
  ;;
esac
export TERM=xterm-256color
export COLORTERM=truecolor
`

func shHook() string { return shBody }

// ContainerEnv is the extra environment on `docker exec`.
// FELDCORE_INIT is the bash rcfile, passed this way so the console does not
// need a file inside the container.
func ContainerEnv() []string {
	return []string{
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"FELDCORE_INIT=" + containerInit(),
	}
}

// ContainerCommand starts bash when the image has it, otherwise a POSIX sh.
// The interactive bash reads ContainerEnv's FELDCORE_INIT as its rcfile.
func ContainerCommand() string {
	return `if command -v bash >/dev/null 2>&1; then
  exec bash -c 'exec bash --rcfile <(printf "%s\n" "$FELDCORE_INIT") -i'
fi
` + shBody + "exec sh -i\n"
}
