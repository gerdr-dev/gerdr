package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// execCmd: `gerdr exec --host <id> -- <command>` runs a command on another computer this one controls and prints its
// output here, exit code passed on. Before it, an agent wanting `ls` there had to start (or message) an agent there
// and wait a turn for every command; the screens' terminal on that computer had no CLI door at all.
// Not auto-approved (sysenv.SafeGerdrCLI): it runs anything, so it asks like any other command.
func execCmd() *cobra.Command {
	var host, cwd string
	var timeout time.Duration
	var stdin, asJSON bool
	cmd := &cobra.Command{
		Use:   i18n.S("exec --host <电脑> [--cwd 目录] -- <命令>", "exec --host <computer> [--cwd dir] -- <command>"),
		Short: i18n.S("在这台电脑控制的另一台电脑上跑一条命令，输出打在这里、退出码照传（gerdr session hosts 列出电脑）", "Run a command on another computer this one controls; its output is printed here and its exit code passed on (gerdr session hosts lists the computers)"),
		Long: i18n.S(
			"命令交给那台电脑的登录 shell（和它的终端一样，PATH 是它 profile 里的），stdout 和 stderr 按出现的顺序合在一起。\n"+
				"--cwd 是那台电脑上的绝对路径或 '~/…'（加引号，不然你的 shell 会把 ~ 换成这台的家目录）；不给就是它的家目录。\n"+
				"没有交互输入：要输入用 --stdin（读这边的 stdin）。超时（--timeout，最长 1h）或者这边 Ctrl-C 都会停掉它和它起的进程。\n"+
				"要一直跑的（dev server、盯日志），在用户开的那台电脑的终端里跑：gerdr pane send / gerdr pane read。",
			"The command goes to that computer's login shell (like its terminal: PATH from its profile); stdout and stderr come\n"+
				"back together, in the order printed. --cwd is a path on that computer, absolute or '~/…' (quoted, or your shell\n"+
				"turns ~ into this computer's home); without it, its home. No interactive input: --stdin sends this side's stdin.\n"+
				"A timeout (--timeout, at most 1h) or Ctrl-C here stops it and everything it started.\n"+
				"For something that keeps running (a dev server, following a log), use a terminal of that computer the user\n"+
				"opened: gerdr pane send / gerdr pane read."),
		Example: "  gerdr session hosts\n" +
			"  gerdr exec --host mini -- uname -a\n" +
			"  gerdr exec --host mini --cwd '~/work/app' -- 'git status --short && npm test'\n" +
			"  cat patch.diff | gerdr exec --host mini --cwd '~/work/app' --stdin -- git apply",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if host == "" {
				return errors.New(i18n.S("要用 --host 说是哪台电脑（gerdr session hosts）；这台电脑上的命令直接在你自己的 shell 里跑", "say which computer with --host (gerdr session hosts); for this computer, run the command in your own shell"))
			}
			body := map[string]any{"command": strings.Join(args, " "), "cwd": cwd, "timeout": timeout.Milliseconds()}
			if stdin {
				b, err := io.ReadAll(io.LimitReader(os.Stdin, 32<<20))
				if err != nil {
					return err
				}
				body["stdin"] = string(b)
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			c.http.Timeout = min(timeout, hub.ExecMaxTimeout) + time.Minute // the command's own limit, plus the way there and back
			var res hub.ExecResult
			if err := c.do("POST", "/api/exec?host="+url.QueryEscape(host), body, &res); err != nil {
				if strings.Contains(err.Error(), "404 page not found") {
					return fmt.Errorf(i18n.S("%s 上的 Gerdr 太旧，没有 exec：让它升级", "the Gerdr on %s is too old for exec: update it"), host)
				}
				return err
			}
			if asJSON {
				printJSON(res)
			} else {
				fmt.Print(res.Output)
				if res.Output != "" && !strings.HasSuffix(res.Output, "\n") {
					fmt.Println()
				}
				if res.TimedOut {
					fmt.Fprintf(os.Stderr, i18n.S("[gerdr exec：超过 %s，停掉了]\n", "[gerdr exec: stopped after %s]\n"), timeout)
				}
			}
			switch {
			case res.TimedOut:
				os.Exit(124) // what timeout(1) exits with
			case res.Code < 0:
				os.Exit(137)
			case res.Code != 0:
				os.Exit(res.Code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", i18n.S("哪台电脑（gerdr session hosts 的 id）", "which computer (an id from gerdr session hosts)"))
	cmd.Flags().StringVar(&cwd, "cwd", "", i18n.S("在那台电脑的哪个目录跑：绝对路径或 '~/…'（默认它的家目录）", "directory on that computer: absolute or '~/…' (default: its home)"))
	cmd.Flags().DurationVar(&timeout, "timeout", hub.ExecDefaultTimeout, i18n.S("最多跑多久（最长 1h）", "how long it may run (at most 1h)"))
	cmd.Flags().BoolVar(&stdin, "stdin", false, i18n.S("把这边的 stdin 交给命令", "pass this side's stdin to the command"))
	cmd.Flags().BoolVar(&asJSON, "json", false, i18n.S("输出 JSON：output、code、timedOut、truncated、cwd、ms", "print JSON: output, code, timedOut, truncated, cwd, ms"))
	return cmd
}
