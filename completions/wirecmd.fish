# Local-only completion. No upstream tools, projected arguments, or JSON values.
function __wirecmd_candidates
    # Newer Fish expands variables/quotes without executing substitutions.
    # Fish 3.7 provides only the native tokenizer; never emulate it with eval.
    set -l words (commandline -xpc 2>/dev/null)
    or set words (commandline -opc)
    set -l executable $words[1]
    set -e words[1]
    set -l current (commandline -ct)
    set -l configs
    set -l positionals
    set -l pending
    set -l prefix 1
    set -l help 0
    set -l input 0
    set -l restricted 0
    set -l help_separator 0

    for word in $words
        if test -n "$pending"
            if test "$pending" = config
                set -a configs --config "$word"
            end
            set pending
            continue
        end
        if test $prefix -eq 0
            set -a positionals "$word"
            continue
        end
        switch "$word"
            case --
                if test $help -eq 1
                    set help_separator 1
                end
                set prefix 0
            case --config -config
                set pending config
                set restricted 1
            case '--config=*' '-config=*'
                set -a configs "$word"
                set restricted 1
            case --format -format --color -color --colour -colour --json -json
                set pending (string replace -r '^-+' '' -- "$word")
                if test "$pending" = json
                    set input 1
                end
            case '--json=*' '-json=*' --stdin -stdin
                set input 1
            case '--format=*' '-format=*' '--color=*' '-color=*' '--colour=*' '-colour=*'
            case --help -help -h
                set help 1
            case --direct -direct
                set restricted 1
            case --version -version
                return
            case '-*'
                return
            case '*'
                set prefix 0
                set -a positionals "$word"
        end
    end

    # Equals-style value completion keeps the option prefix in the candidate.
    set -l value_prefix
    if test $prefix -eq 1; and test -z "$pending"
        switch "$current"
            case '--config=*' '--format=*' '--color=*' '--colour=*' '--json=*'
                set pending (string replace -r '^--([^=]+)=.*' '$1' -- "$current")
                set value_prefix "--$pending="
        end
    end
    if test -n "$pending"
        switch "$pending"
            case config
                set -l path "$current"
                if test -n "$value_prefix"
                    set path (string replace -- "$value_prefix" '' "$path")
                end
                for candidate in (__fish_complete_path "$path")
                    printf '%s%s\n' "$value_prefix" "$candidate"
                end
            case format
                printf '%s%s\n' "$value_prefix" auto "$value_prefix" json "$value_prefix" pretty
            case color colour
                printf '%s%s\n' "$value_prefix" auto "$value_prefix" always "$value_prefix" never
        end
        return
    end

    # The removed `--help -- SERVER` escape must not look like a static-help
    # path to completion. Once that prefix separator follows help, offer
    # nothing rather than suggesting native groups.
    if test $help_separator -eq 1
        return
    end

    if test (count $positionals) -eq 0
        if test $prefix -eq 1
            printf '%b\n' '--config\tKDL file (repeatable)' '--direct\tOne-shot execution' '--json\tTool argument object' '--stdin\tRead tool arguments' '--help\tShow help' '-h\tShow help' '--version\tStandalone version' '--format\tOutput format' '--color\tColor mode' '--colour\tColor mode'
        end
        if test $input -eq 0
            printf '%b\n' 'auth\tOAuth credentials'
            if test $restricted -eq 0
                printf '%b\n' 'daemon\tDaemon administration' 'config\tWorkspace trust'
            end
            printf '%b\n' 'lsp\tNative LSP navigation and inspection'
            printf '%b\n' 'mcp\tConfigured MCP capabilities'
        end
        return
    end

    if test $input -eq 1
        return
    end
    switch "$positionals[1]"
        case auth
            if test (count $positionals) -eq 1
                printf '%s\n' login status logout
            else if test (count $positionals) -eq 2; and contains -- "$positionals[2]" login status logout
                command "$executable" --completion-servers $configs 2>/dev/null
            end
        case daemon
            if test $restricted -eq 0; and test (count $positionals) -eq 1
                printf '%s\n' run status reload
            end
        case config
            if test $restricted -eq 1
                return
            end
            if test (count $positionals) -eq 1
                printf '%s\n' trust untrust
            else if test (count $positionals) -eq 2; and contains -- "$positionals[2]" trust untrust
                if test "$positionals[2]" = trust
                    printf '%s\n' status list
                end
                __fish_complete_directories "$current"
            else if test (count $positionals) -eq 3; and test "$positionals[2]" = trust; and test "$positionals[3]" = status
                __fish_complete_directories "$current"
            end
        case lsp
            if test (count $positionals) -eq 1
                printf '%s\n' definition declaration type-definition implementation references hover signature-help document-symbols workspace-symbols status
            else if test (count $positionals) -eq 2
                switch "$positionals[2]"
                    case definition declaration type-definition implementation hover signature-help
                        printf '%s\n' --file --line --column --help
                    case references
                        printf '%s\n' --file --line --column --include-declaration --help
                    case document-symbols
                        printf '%s\n' --file --help
                    case workspace-symbols
                        printf '%s\n' --query --help
                    case status
                        printf '%s\n' --file --help
                end
            end
        case mcp
            if test (count $positionals) -eq 1
                set -l escaped_alias 0
                set -l double_dash_alias 0
                for server in (command "$executable" --completion-servers $configs 2>/dev/null)
                    switch "$server"
                        case --help -h
                            set escaped_alias 1
                            # `mcp -- --help` and `mcp -- -h` are escapes.
                        case --
                            set double_dash_alias 1
                            printf '%s\n' "$server"
                        case '*'
                            printf '%s\n' "$server"
                    end
                end
                if test $escaped_alias -eq 1; and test $double_dash_alias -eq 0
                    printf '%b\n' '--\tEscape a server alias named --help or -h'
                end
            else if test (count $positionals) -eq 2
                if test "$positionals[2]" = --
                    set -l double_dash_alias 0
                    for server in (command "$executable" --completion-servers $configs 2>/dev/null)
                        if test "$server" = --help; or test "$server" = -h
                            printf '%s\n' "$server"
                        else if test "$server" = --
                            set double_dash_alias 1
                        end
                    end
                    if test $double_dash_alias -eq 1
                        printf '%s\n' tool resources resource-templates resource
                    end
                else if not contains -- "$positionals[2]" --help -h
                    printf '%s\n' tool resources resource-templates resource
                end
            else if test (count $positionals) -eq 3; and test "$positionals[2]" = --; and contains -- "$positionals[3]" --help -h
                printf '%s\n' tool resources resource-templates resource
            end
    end
end

complete -c wirecmd -f -a '(__wirecmd_candidates)'
