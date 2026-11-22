package codex

import (
	"fmt"
	"io"
	"slices"

	"github.com/dinoderek/engine/internal/hosts"
)

// pluginDisk is what a host keeps of engine's plugin in its Codex homes, as
// the retention commands (retain.go) see it.
type pluginDisk struct {
	// cache is each home's version directories, by home; unset, a home has
	// the version its plugin list shows. kept is what keepCommand copied
	// aside, and marked when restoreCommand last ran, in Unix seconds.
	cache  map[string][]string
	kept   map[string][]string
	marked map[string]int64
	// windows are when session engine's windows started; noTmux leaves
	// tmux off the PATH; now is the host's clock.
	windows []int64
	noTmux  bool
	now     int64
	// keepFail and restoreFail fail those commands.
	keepFail, restoreFail bool
}

// versions are the plugin's versions in home.
func (c *codexState) versions(home string) []string {
	if v, ok := c.disk.cache[home]; ok {
		return v
	}
	listed := cond(home == c.userHome, c.userPlugin, c.plugin)
	if listed == "" {
		return nil
	}
	return []string{listed}
}

func (c *codexState) setVersions(home string, v []string) {
	if c.disk.cache == nil {
		c.disk.cache = map[string][]string{}
	}
	c.disk.cache[home] = v
}

// added is Codex's plugin add in home: only the version it adds is left.
func (c *codexState) added(home, version string) {
	c.setVersions(home, cond2(version != "", []string{version}, nil))
}

func cond2(ok bool, yes, no []string) []string {
	if ok {
		return yes
	}
	return no
}

// retain answers the retention commands, which only engine's home takes.
func (c *codexState) retain(b *box, cmd string, out, errOut io.Writer) (int, bool) {
	if cmd == windowStartsCommand {
		if c.disk.noTmux {
			return 3, true
		}
		for _, w := range c.disk.windows {
			fmt.Fprintln(out, w)
		}
		return 0, true
	}
	return c.retainIn(b, c.engineHome(), cmd, out, errOut)
}

func (c *codexState) retainIn(b *box, home, cmd string, out, errOut io.Writer) (int, bool) {
	word := hosts.ShellWord(home)
	switch cmd {
	case keepCommand(word):
		b.mutate(cmd)
		if c.disk.keepFail {
			fmt.Fprint(errOut, "cp: No space left on device")
			return 1, true
		}
		if c.disk.kept == nil {
			c.disk.kept = map[string][]string{}
		}
		c.disk.kept[home] = slices.Clone(c.versions(home))
		return 0, true
	case restoreCommand(word):
		b.mutate(cmd)
		if c.disk.restoreFail {
			fmt.Fprint(errOut, "cp: No space left on device")
			return 1, true
		}
		have := slices.Clone(c.versions(home))
		for _, v := range c.disk.kept[home] {
			if !slices.Contains(have, v) {
				have = append(have, v)
			}
		}
		c.setVersions(home, have)
		delete(c.disk.kept, home)
		if c.disk.marked == nil {
			c.disk.marked = map[string]int64{}
		}
		c.disk.marked[home] = c.disk.now
		return 0, true
	case "cat " + installedMark(word):
		at, ok := c.disk.marked[home]
		if !ok {
			return 1, true
		}
		fmt.Fprintln(out, at)
		return 0, true
	}
	for _, v := range c.versions(home) {
		if cmd == pruneCommand(word, v) {
			b.mutate(cmd)
			c.setVersions(home, []string{v})
			delete(c.disk.marked, home)
			return 0, true
		}
	}
	return 0, false
}
