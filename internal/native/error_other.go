//go:build !windows

package native

import "log"

func ShowError(title, message string) { log.Printf("%s: %s", title, message) }
