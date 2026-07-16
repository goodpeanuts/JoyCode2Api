package main

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
	"github.com/vibe-coding-labs/JoyCodeProxy/pkg/joycode"
)

// Version is injected at release time via -ldflags "-X main.Version=<tag>".
// Empty for local/dev builds, where resolveVersion() falls back to VCS info.
var Version = ""

// resolveVersion returns the release version when injected, otherwise derives
// a dev version string from the embedded VCS build info (Go 1.18+, -buildvcs).
func resolveVersion() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var rev, ts string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			ts = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	v := "dev-" + rev
	if dirty {
		v += "-dirty"
	}
	if ts != "" {
		v += " (" + ts + ")"
	}
	return v
}

var versionCmd = &cobra.Command{
	Use:     "version",
	Short:   "显示版本信息",
	GroupID: "query",
	Example: `  jcproxy version`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("JoyCode Proxy %s\n", Version)
		fmt.Printf("  JoyCode API: %s\n", joycode.ClientVersion)
		fmt.Printf("  Go:          %s\n", runtime.Version())
	},
}

func init() {
	Version = resolveVersion()
	rootCmd.AddCommand(versionCmd)
}
