package core

import "regexp"

var tmuxNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_-]{0,63}$`)

func validTmuxName(name string) bool { return tmuxNamePattern.MatchString(name) }
