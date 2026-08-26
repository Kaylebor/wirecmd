package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestVersionNormalizesOnlyUnversionedBuilds(t *testing.T) {
	for _, test := range []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{name: "missing build info", want: developmentVersion},
		{name: "nil build info", ok: true, want: developmentVersion},
		{name: "empty version", info: &debug.BuildInfo{}, ok: true, want: developmentVersion},
		{name: "development version", info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, ok: true, want: developmentVersion},
		{name: "local checkout pseudo version", info: &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260826120000-0123456789ab+dirty"}, Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}}}, ok: true, want: developmentVersion},
		{name: "tagged version", info: &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}}, ok: true, want: "v0.1.0"},
		{name: "pseudo version", info: &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260826120000-0123456789ab"}}, ok: true, want: "v0.0.0-20260826120000-0123456789ab"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := version(test.info, test.ok); got != test.want {
				t.Fatalf("version() = %q, want %q", got, test.want)
			}
		})
	}
}
