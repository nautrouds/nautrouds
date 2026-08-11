package version

import "runtime/debug"

var Version = ""

func Get() string {
	if Version != "" {
		return Version
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				revision := setting.Value
				if len(revision) > 7 {
					revision = revision[:7]
				}
				if dirty(info) {
					revision += "-dirty"
				}
				return revision
			}
		}
	}

	return "0.0.0"
}

func dirty(info *debug.BuildInfo) bool {
	for _, setting := range info.Settings {
		if setting.Key == "vcs.modified" && setting.Value == "true" {
			return true
		}
	}
	return false
}
