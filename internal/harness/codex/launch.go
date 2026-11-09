package codex

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// launcher is one Codex session's launch.
type launcher struct {
	Adapter
	profile string
}

func (l launcher) Trust(dir, clone string, joined bool) error {
	var err error
	if joined {
		err = TrustFolder(l.engineConfig(), dir)
	} else {
		err = WriteProfile(l.home(), l.profile, dir, clone)
	}
	if err != nil {
		return fmt.Errorf("trust %s for Codex: %w", dir, err)
	}
	return nil
}

// Release deletes the session's profile and removes dir's trust from
// engine's config.toml, whichever Trust wrote.
func (l launcher) Release(dir string) error {
	_, err := RemoveProfile(l.home(), l.profile)
	if dir != "" {
		err = errors.Join(err, UntrustFolder(l.engineConfig(), dir))
	}
	return err
}

func (l launcher) Env(joined bool) string {
	if joined {
		return "export CODEX_HOME=" + hosts.ShellWord(l.engineHome()) + " && "
	}
	if dir := l.env.Var("CODEX_HOME"); dir != "" {
		return "export CODEX_HOME=" + hosts.ShellWord(dir) + " && "
	}
	return "unset CODEX_HOME && "
}

// Args join the daemon over its default socket in engine's Codex home, or
// select the session's profile. A joined session names its folder with -C:
// without it, the daemon starts the thread in its own working directory.
// Hands-off launches hide the rate-limit model nudge, keeping the model.
func (l launcher) Args(s harness.Launch) string {
	reminder := ""
	if s.Unattended {
		reminder = " -c notice.hide_rate_limit_model_nudge=true"
	}
	if s.Joined {
		return reminder + " -C " + hosts.ShellWord(s.Dir) + " --dangerously-bypass-hook-trust -c " + sessionVars(s) +
			" --remote unix:// -- " + hosts.ShellWord(s.Kickoff)
	}
	return reminder + " -p " + hosts.ShellWord(l.profile) + " --dangerously-bypass-hook-trust -- " + hosts.ShellWord(s.Kickoff)
}

// sessionVars is the shell for the -c value that gives a joined session's
// commands what the window exports: the agent runs in the daemon, whose
// environment has none of it. TMUX and TMUX_PANE, which the session closes
// its own window with, come from the window's shell as it starts.
func sessionVars(s harness.Launch) string {
	vars := []string{session.RunDirVar + "=" + TOMLString(s.RunDir), session.HarnessVar + "=" + TOMLString(Name)}
	if s.ServerURL != "" {
		vars = append(vars, "ENGINE_SERVER_URL="+TOMLString(s.ServerURL),
			"ENGINE_SERVER_TOKEN_FILE="+TOMLString(s.TokenFile),
			"ENGINE_ATTEMPT_ID="+TOMLString(s.Attempt))
	}
	if s.Unattended {
		vars = append(vars, session.UnattendedVar+`="1"`)
	}
	return hosts.ShellWord("shell_environment_policy.set={"+strings.Join(vars, ",")+`,TMUX="`) + `"$TMUX"` +
		hosts.ShellWord(`",TMUX_PANE="`) + `"$TMUX_PANE"` + hosts.ShellWord(`"}`)
}
