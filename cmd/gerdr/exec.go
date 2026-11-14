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
				"没有交互输入：要输入用 --stdin（读这边的 stdin，原样的字节、不限大小，到了那边会核对字节数和 sha256，对不上就退出码 75）。\n"+
				"传文件 / 目录用 gerdr cp，不用 cat | --stdin。超时（--timeout，最长 1h）或者这边 Ctrl-C 都会停掉它和它起的进程。\n"+
				"要一直跑的（dev server、盯日志），在用户开的那台电脑的终端里跑：gerdr pane send / gerdr pane read。",
			"The command goes to that computer's login shell (like its terminal: PATH from its profile); stdout and stderr come\n"+
				"back together, in the order printed. --cwd is a path on that computer, absolute or '~/…' (quoted, or your shell\n"+
				"turns ~ into this computer's home); without it, its home. No interactive input: --stdin sends this side's stdin\n"+
				"(raw bytes, any size; the byte count and sha256 are checked over there, exit code 75 if they don't match).\n"+
				"To copy files or folders use gerdr cp, not cat | --stdin.\n"+
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
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			c.http.Timeout = min(timeout, hub.ExecMaxTimeout) + time.Minute // the command's own limit, plus the way there and back
			var res hub.ExecResult
			var sent *hashCounter
			if stdin {
				// Streamed as the raw body: any size, bytes as they are (a JSON string mangled binaries and the old
				// 32 MB cut went unnoticed). The other side counts and hashes what arrived; checked below.
				sent = newHashCounter()
				q := url.Values{"host": {host}, "stdin": {"1"}, "command": {strings.Join(args, " ")}, "cwd": {cwd}, "timeout": {fmt.Sprint(timeout.Milliseconds())}}
				_, err = c.raw("POST", "/api/exec?"+q.Encode(), io.TeeReader(os.Stdin, sent), c.http.Timeout, &res)
				if err == nil && res.Stdin == nil && !res.TimedOut {
					err = fmt.Errorf(i18n.S("%s 上的 Gerdr 太旧，--stdin 不可靠：让它升级", "the Gerdr on %s is too old for a reliable --stdin: update it"), host)
				}
			} else {
				err = c.do("POST", "/api/exec?host="+url.QueryEscape(host), map[string]any{"command": strings.Join(args, " "), "cwd": cwd, "timeout": timeout.Milliseconds()}, &res)
			}
			if err != nil {
				if strings.Contains(err.Error(), "404 page not found") || strings.Contains(err.Error(), "invalid character") {
					return fmt.Errorf(i18n.S("%s 上的 Gerdr 太旧（没有 exec，或 --stdin 只收文本）：让它升级", "the Gerdr on %s is too old (no exec, or --stdin as text only): update it"), host)
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
			if sent != nil && res.Stdin != nil && (res.Stdin.Bytes != sent.n.Load() || res.Stdin.Sha256 != sent.sum()) {
				fmt.Fprintf(os.Stderr, i18n.S("[gerdr exec：stdin 没有完整到达：发了 %d 字节，那边收到 %d 字节（sha256 %s / %s）]\n", "[gerdr exec: stdin didn't arrive intact: sent %d bytes, %d arrived (sha256 %s / %s)]\n"),
					sent.n.Load(), res.Stdin.Bytes, sent.sum(), res.Stdin.Sha256)
				os.Exit(75) // EX_TEMPFAIL: the command may have seen a cut input
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
