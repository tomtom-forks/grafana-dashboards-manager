package utils

import "runtime/debug"

func BuildInfoString() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.String()
	}
	return "(unknown)"
}
