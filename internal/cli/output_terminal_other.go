//go:build !linux && !darwin

package cli

import "os"

func outputFileIsTerminal(file *os.File) bool { return false }
