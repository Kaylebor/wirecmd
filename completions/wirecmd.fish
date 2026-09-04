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
    set -l separator 0
    set -l help 0
    set -l input 0
    set -l restricted 0

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
                set prefix 0
                set separator 1
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

    if test (count $positionals) -eq 0
        if test $prefix -eq 1
            printf '%b\n' '--config\tKDL file (repeatable)' '--direct\tOne-shot execution' '--json\tTool argument object' '--stdin\tRead tool arguments' '--help\tShow help' '-h\tShow help' '--version\tStandalone version' '--format\tOutput format' '--color\tColor mode' '--colour\tColor mode'
        end
        if test $separator -eq 0; and test $input -eq 0
            printf '%b\n' 'auth\tOAuth credentials'
            if test $restricted -eq 0
                printf '%b\n' 'daemon\tDaemon administration' 'config\tWorkspace trust'
            end
        end
        command "$executable" --completion-servers $configs 2>/dev/null
        return
    end

    # With help's explicit separator, names always belong to servers/tools.
    if test $help -eq 1; and test $separator -eq 1
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
    end
end

complete -c wirecmd -f -a '(__wirecmd_candidates)'
