# >>> Meridian Deck OSC 7 >>>
if [[ $- == *i* ]]; then
    __meridian_report_directory() {
        local previous=$? LC_ALL=C rest="$PWD" escaped='' ch byte
        while [[ -n $rest ]]; do
            ch=${rest%"${rest#?}"}
            rest=${rest#?}
            case "$ch" in
                [a-zA-Z0-9._~/-]) escaped+="$ch" ;;
                *) printf -v byte '%%%02X' "'$ch"; escaped+="$byte" ;;
            esac
        done
        printf '\033]7;file://%s\033\\' "$escaped"
        if [[ -n ${BASH_VERSION-} ]]; then return "$previous"; fi
        return 0
    }
    if [[ -n ${ZSH_VERSION-} ]]; then
        autoload -Uz add-zsh-hook && add-zsh-hook precmd __meridian_report_directory
    elif [[ ${PROMPT_COMMAND[*]-} != *__meridian_report_directory* ]]; then
        if (( BASH_VERSINFO[0] > 5 || (BASH_VERSINFO[0] == 5 && BASH_VERSINFO[1] >= 1) )); then
            PROMPT_COMMAND+=(__meridian_report_directory)
        else
            if [[ -n ${PROMPT_COMMAND-} ]]; then PROMPT_COMMAND+=$'\n'; fi
            PROMPT_COMMAND+=__meridian_report_directory
        fi
    fi
fi
# <<< Meridian Deck OSC 7 <<<
