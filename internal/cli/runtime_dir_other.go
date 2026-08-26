//go:build !darwin

package cli

func defaultRuntimeDirectory() string {
	return ""
}
