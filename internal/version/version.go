package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// buildRevision is set via -ldflags -X at build time (see scripts/build.sh and
// scripts/pre-release.sh). debug.ReadBuildInfo's embedded VCS info is unreliable
// for builds done from a git worktree, so build scripts pass the correct
// revision explicitly rather than relying on it.
var buildRevision string

// Version is the build version, resolved at startup.
var Version = computeVersion()

func computeVersion() string {
	if buildRevision != "" {
		return buildRevision
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return buildVersion(info)
}

func buildVersion(info *debug.BuildInfo) string {
	v := info.Main.Version
	hasModVer := v != "" && v != "(devel)" && !strings.HasPrefix(v, "v0.0.0") && !strings.Contains(v, "-0.")

	rev, dirty := extractVCSInfo(info.Settings)
	if rev != "" && dirty {
		rev += "+dirty"
	}

	switch {
	case hasModVer && rev != "":
		return fmt.Sprintf("%s (%s)", v, rev)
	case hasModVer:
		return v
	case rev != "":
		return rev
	default:
		return "dev"
	}
}

func extractVCSInfo(settings []debug.BuildSetting) (rev string, dirty bool) {
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				rev = s.Value[:7]
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return
}
