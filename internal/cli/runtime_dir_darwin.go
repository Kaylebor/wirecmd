//go:build darwin

package cli

import "os"

func defaultRuntimeDirectory() string {
	return os.TempDir()
}
