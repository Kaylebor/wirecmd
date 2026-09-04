#!/usr/bin/env fish
# Native integration test; Fish is a required prerequisite, not a skipped test.
set -l root (realpath (dirname (status filename))/..)
set -l temporary (mktemp -d)
or exit 1
function __wirecmd_test_cleanup --on-event fish_exit --inherit-variable temporary
    rm -rf -- "$temporary"
end

go build -o "$temporary/wirecmd" "$root"
or exit 1
set -gx PATH "$temporary" $PATH
set -gx XDG_CONFIG_HOME "$temporary/global"
set -gx XDG_STATE_HOME "$temporary/state"
set -gx XDG_RUNTIME_DIR "$temporary/runtime-not-created"
set -l config "$temporary/base config.kdl"
set -l stronger "$temporary/override.kdl"
printf '%s\n' 'wirecmd {' 'server "alpha" { scope "workspace"; stdio "never-started" }' 'server "space name" { scope "workspace"; stdio "never-started" }' 'server "$(touch SHOULD_NOT_EXIST)" { scope "workspace"; stdio "never-started" }' 'server "quote\"name" { scope "workspace"; stdio "never-started" }' '}' >"$config"
printf '%s\n' 'wirecmd { server "beta" { scope "workspace"; stdio "never-started" } }' >"$stronger"
source "$root/completions/wirecmd.fish"
or exit 1

function candidates
    set -g results (complete -C "$argv[1]" | string replace -r '\t.*$' '')
end
function require
    if not contains -- "$argv[1]" $results
        printf 'Missing %s in %s\n' (string escape -- "$argv[1]") (string join ', ' -- $results) >&2
        exit 1
    end
end
function reject
    if contains -- "$argv[1]" $results
        printf 'Unexpected completion %s\n' (string escape -- "$argv[1]") >&2
        exit 1
    end
end
function empty
    if test (count $results) -ne 0
        printf 'Expected no candidates, got %s\n' (string join ', ' -- $results) >&2
        exit 1
    end
end

set -l prefix "wirecmd --config "(string escape -- "$config")
candidates "$prefix "
require alpha
require 'space name'
require '$(touch SHOULD_NOT_EXIST)'
require 'quote"name'
require --format
require auth
reject daemon # --config is forbidden for daemon administration

candidates "$prefix --config=$stronger "
require alpha
require beta
candidates "$prefix --help -- "
require alpha
reject --format
reject auth
candidates "$prefix --help -- daemon "
empty
candidates "$prefix alpha "
empty
candidates "$prefix alpha tool -- "
empty
candidates "$prefix alpha tool --format "
empty
candidates "$prefix --json '{}' auth "
empty
candidates "$prefix auth "
require login
require status
require logout
candidates "$prefix auth login "
require alpha
candidates 'wirecmd --format pretty daemon '
require reload
reject pretty
candidates 'wirecmd --help config '
require trust
require untrust
candidates 'wirecmd config trust '
require status
require list
candidates 'wirecmd config trust list '
empty
candidates 'wirecmd --format '
require auto
require json
require pretty
candidates 'wirecmd --colour='
require --colour=auto
require --colour=always
require --colour=never
candidates "wirecmd --config $temporary/over"
require "$stronger"
candidates "wirecmd --config=$temporary/over"
require "--config=$stronger"
candidates "wirecmd config trust $temporary/glo"
empty # no global directory has been created
mkdir "$temporary/trusted-directory"
or exit 1
candidates "wirecmd config trust $temporary/tru"
require "$temporary/trusted-directory/"
candidates "wirecmd config trust status $temporary/tru"
require "$temporary/trusted-directory/"
candidates "wirecmd config untrust $temporary/tru"
require "$temporary/trusted-directory/"
candidates "wirecmd --config $temporary/missing "
reject alpha
candidates 'wirecmd --version '
empty
test ! -e "$XDG_STATE_HOME"; or exit 1
test ! -e "$XDG_RUNTIME_DIR"; or exit 1
test ! -e SHOULD_NOT_EXIST; or exit 1
printf 'Fish completion integration passed\n'
