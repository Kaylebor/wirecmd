//go:build linux

package cli

import "testing"

func TestBrowserOpenCommandUsesXDGOpen(t *testing.T) {
	command, err := browserOpenCommand("https://example.test/authorize")
	if err != nil {
		t.Fatal(err)
	}
	if len(command.Args) != 2 || command.Args[0] != "xdg-open" || command.Args[1] != "https://example.test/authorize" {
		t.Fatalf("browser command = %#v", command.Args)
	}
}
