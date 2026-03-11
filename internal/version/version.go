package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

var (
	Version   = "dev"
	Commit    = ""
	BuildDate = ""
)

type Details struct {
	Version   string
	Commit    string
	BuildDate string
	Dirty     bool
}

func Current() Details {
	d := Details{
		Version:   strings.TrimSpace(Version),
		Commit:    strings.TrimSpace(Commit),
		BuildDate: strings.TrimSpace(BuildDate),
	}

	if bi, ok := debug.ReadBuildInfo(); ok {
		if (d.Version == "" || d.Version == "dev") && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			d.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if d.Commit == "" {
					d.Commit = strings.TrimSpace(s.Value)
				}
			case "vcs.time":
				if d.BuildDate == "" {
					d.BuildDate = strings.TrimSpace(s.Value)
				}
			case "vcs.modified":
				d.Dirty = strings.EqualFold(strings.TrimSpace(s.Value), "true")
			}
		}
	}

	if d.Version == "" {
		d.Version = "dev"
	}
	return d
}

func Info(prog string) string {
	d := Current()
	name := strings.TrimSpace(prog)
	if name == "" {
		name = "myworktree"
	}

	line := fmt.Sprintf("%s %s", name, d.Version)
	if d.Commit != "" {
		line += fmt.Sprintf(" (%s)", shortCommit(d.Commit))
	}
	if d.Dirty {
		line += " dirty"
	}
	if d.BuildDate != "" {
		line += fmt.Sprintf(" built %s", d.BuildDate)
	}
	return line
}

func shortCommit(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}
