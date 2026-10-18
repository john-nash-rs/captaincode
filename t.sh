#!/bin/sh
# captaincode manual test kit.
#
#   ./t.sh build     compile the local binary
#   ./t.sh eval      run the live evals against your real key
#   ./t.sh doctor    diagnostics in a throwaway home
#   ./t.sh once      one-shot question, no config file
#   ./t.sh fresh     full new-user journey, isolated home
#   ./t.sh tui       launch the interface to try by hand
#
# Everything runs as ./bin/captaincode, never the Homebrew build, which is an
# older release and will not have these commands.
#
# The credential comes from CAPTAINCODE_EVAL_KEY, or from the file named by
# CAPTAINCODE_EVAL_KEY_FILE. Nothing is baked in here: the evals skip rather
# than fail when neither is set, so this works on a machine with no key.

set -e
cd "$(dirname "$0")"
BIN=./bin/captaincode

# key prints the credential, reading nothing from this repository.
#
# It resolves its own inputs rather than depending on variables set above, so it
# can be lifted out and exercised on its own.
key() {
	if [ -n "$CAPTAINCODE_EVAL_KEY" ]; then
		printf '%s' "$CAPTAINCODE_EVAL_KEY"
		return 0
	fi
	_file=${CAPTAINCODE_EVAL_KEY_FILE:-$HOME/.captaincode-eval-key}
	if [ ! -r "$_file" ]; then
		echo "no credential: set CAPTAINCODE_EVAL_KEY or CAPTAINCODE_EVAL_KEY_FILE" >&2
		return 1
	fi
	# The file may hold a bare key or api-key="…".
	_value=$(sed -n 's/^api-key="\(.*\)"$/\1/p' "$_file")
	[ -n "$_value" ] || _value=$(head -1 "$_file")
	printf '%s' "$_value"
}

case "${1:-tui}" in
build)
	go build -o "$BIN" ./cmd/captaincode
	echo "built $BIN"
	;;

eval)
	# CAPTAINCODE_EVAL_KEY_FILE tells the evals where the credential is. They
	# skip rather than fail when it is absent.
	CAPTAINCODE_EVAL_KEY_FILE="$KEYFILE" go test ./evals/ -run TestLive -v -count=1
	;;

doctor)
	# A scratch home, so this shows what a brand new machine looks like
	# rather than whatever is already in your real settings.
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	HOME=$tmp XDG_CONFIG_HOME=$tmp/.config OPENROUTER_API_KEY= \
		"$BIN" doctor --provider openrouter
	;;

once)
	OPENROUTER_API_KEY=$(key) "$BIN" ./testdata/project \
		--provider openrouter --ide=false --model openrouter/free \
		--question "Reply with exactly: ONLINE"
	;;

fresh)
	# The whole new-user journey, start to finish, with no inherited key:
	# nothing configured -> doctor explains -> follow its instructions ->
	# an ordinary question works with no flags.
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	mkdir -p "$tmp/work" "$tmp/Library/Application Support/captaincode"
	echo '{"version":"2.5.0"}' > "$tmp/work/package.json"

	run() { HOME=$tmp XDG_CONFIG_HOME=$tmp/.config OPENROUTER_API_KEY= "$BIN" "$@"; }

	echo "--- 1. nothing configured yet ---"
	run doctor --provider openrouter

	echo
	echo "--- 2. do what it just told you to do ---"
	echo '{"model":"openai/gpt-4o-mini"}' > "$tmp/.captaincode.json"
	umask 077
	key > "$tmp/Library/Application Support/captaincode/openrouter.key"

	echo "--- 3. doctor now passes ---"
	run doctor --provider openrouter

	echo
	echo "--- 4. ask a question, no --model flag ---"
	run "$tmp/work" --provider openrouter --ide=false \
		--question "Reply with exactly: ONLINE"
	;;

tui)
	# Interactive. Try: / to open the menu, up/down or click, enter.
	# Then /connect, /status, /help.
	OPENROUTER_API_KEY=$(key) exec "$BIN" "${@:-./testdata/project}" --provider openrouter
	;;

*)
	echo "usage: $0 {build|eval|doctor|once|fresh|tui [dir]}"
	exit 2
	;;
esac