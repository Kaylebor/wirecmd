// Package buildinfo reports the version embedded in the Wirecmd executable.
package buildinfo

import "runtime/debug"

const developmentVersion = "dev"

var readBuildInfo = debug.ReadBuildInfo

// Version returns the module version embedded by the Go toolchain. Local,
// unversioned builds consistently identify themselves as dev.
func Version() string {
	info, ok := readBuildInfo()
	return version(info, ok)
}

func version(info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return developmentVersion
	}
	// Builds made from a VCS checkout carry vcs.* settings. Go 1.24 and newer
	// may also synthesize a pseudo-version for them, but they are still local
	// development builds rather than an installed module release.
	for _, setting := range info.Settings {
		if setting.Key == "vcs" {
			return developmentVersion
		}
	}
	return info.Main.Version
}
