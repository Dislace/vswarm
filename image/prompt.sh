# shellcheck shell=bash
export PATH="$HOME/.local/bin:$HOME/.bun/bin:$PATH"
export NPM_CONFIG_PREFIX="$HOME/.local"
if [ -n "$VSWARM_USER" ]; then
  PS1='\[\e[1;32m\]'"$VSWARM_USER"'@\h\[\e[0m\]:\[\e[1;34m\]\w\[\e[0m\]\$ '
fi
