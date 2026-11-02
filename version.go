package main

import "runtime/debug"

// version is set by the release build's ldflags; go install leaves it empty.
var version = ""

func currentVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
