package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// Version is the build version, populated at startup from embedded VCS build info.
var Version = "dev"

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	Version = buildVersion(info)
}

func buildVersion(info *debug.BuildInfo) string {
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				rev = s.Value[:7]
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if dirty {
		rev += "+dirty"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" && !strings.HasPrefix(v, "v0.0.0") && !strings.Contains(v, "-0.") {
		return fmt.Sprintf("%s (%s)", v, rev)
	}
	return rev
}
