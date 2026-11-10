package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// gerdr wait: waiting for a state (docs/dev/EVENTS.md, "Waiting for a state"). An add-on reports what a thing is like
// (an MR: opened / merged, its pipeline, how many review comments); a session waits until a condition on it holds
// and is woken with a message. Checked against the state as it is, so waiting late misses nothing.
func waitCmd() *cobra.Command {
	var until, note, session string
	cmd := &cobra.Command{
		Use:   "wait <source> <key> --until '<condition>'",
		Short: i18n.S("等一个东西的状态满足条件（MR 有新意见、流水线挂了、合并了……），满足了发一条消息给这个会话", "wait until a thing's state meets a condition (an MR got comments, its pipeline failed, it merged…); then this session gets a message"),
		Long: i18n.S(`等一个东西的状态满足条件，满足了发一条消息给会话（默认你自己），然后这次等待就结束。
插件报告东西现在的样子（gerdr wait sources 看有哪些、各有什么字段），Gerdr 拿条件去对：已经满足的当场叫醒，
所以晚一点才等也不会错过。要接着等，就照叫醒消息里现在的值写下一个条件，再等一次。

  gerdr wait gitlab 'gitlab.example.com/group/repo!34' \
    --until "state != 'opened' || pipeline == 'failed' || reviews > 2" --note "处理意见和流水线；合并后清理"

条件是一个 JavaScript 表达式：状态的每个字段都是变量（$ 是整个状态），值为真就叫醒。字符串要加引号。
列表可以过滤：notes.some(n => n.author != me && n.resolvable && !n.resolved && n.id > 1233511)
只能读状态，碰不到文件和网络，每次判断最多 50ms。
计数类的写成「比现在多」：reviews > <现在的个数>，不然处理完再等会马上又被叫醒。
等待属于这段对话：会话关掉、删掉或 /clear 之后就取消了（Gerdr 重启不影响）。`,
			`Wait until a thing's state meets a condition; then the session (default: you) gets a message, and the wait is over.
Add-ons report what things are like now (gerdr wait sources lists them and their fields); Gerdr checks the
condition against that: one that holds already wakes at once, so waiting late misses nothing. To wait for what comes
next, write the next condition from the values in the message and wait again.

  gerdr wait gitlab 'gitlab.example.com/group/repo!34' \
    --until "state != 'opened' || pipeline == 'failed' || reviews > 2" --note "handle comments and pipelines; clean up once merged"

A condition is one JavaScript expression: each of the state's fields is a variable ($ is the whole state); it wakes
you when the value is true. Strings need quotes. Lists can be filtered:
notes.some(n => n.author != me && n.resolvable && !n.resolved && n.id > 1233511)
It reads only the state (no files, no network), 50ms per check.
A wait belongs to this conversation: closing, deleting or /clear-ing the session drops it (a Gerdr restart doesn't).
Write counts as "more than now": reviews > <the count now>, or waiting again after handling them wakes at once.`),
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			if until == "" {
				fail(errors.New(i18n.S("要给 --until：等到什么条件成立", "--until is required: the condition to wait for")))
			}
			c, err := newAPIClient()
			fail(err)
			target, err := c.reportTarget(session)
			fail(err)
			var out struct {
				Wait   json.RawMessage `json:"wait"`
				Woken  bool            `json:"woken"`
				Status struct {
					Now     map[string]any `json:"now"`
					Holds   *bool          `json:"holds"`
					Warning string         `json:"warning"`
				} `json:"status"`
			}
			var raw json.RawMessage
			fail(c.do("POST", "/api/waits", map[string]any{"source": args[0], "key": args[1], "until": until, "note": note, "session": target}, &raw))
			_ = json.Unmarshal(raw, &out)
			printJSON(raw)
			switch {
			case out.Woken:
				fmt.Fprintln(os.Stderr, i18n.S("已经成立了：消息已经发过去，这次等待结束了。", "It holds already: the message was sent, and this wait is over."))
			case out.Status.Warning != "":
				fmt.Fprintln(os.Stderr, i18n.S("注意：", "Note: ")+out.Status.Warning)
			case out.Status.Now == nil:
				fmt.Fprintln(os.Stderr, i18n.S("在等：插件还没报来它的状态，报来就按条件判断。", "Waiting: the add-on hasn't reported its state yet; it will be checked as soon as it does."))
			default:
				fmt.Fprintln(os.Stderr, i18n.S("在等：现在还不成立，状态一满足就叫醒。现在的值见 status.now。", "Waiting: it doesn't hold yet; you'll be woken when it does. The values now are in status.now."))
			}
		},
	}
	cmd.Flags().StringVar(&until, "until", "", i18n.S("等到这个 JS 表达式为真：state != 'opened' || reviews > 2", "wait until this JS expression is true: state != 'opened' || reviews > 2"))
	cmd.Flags().StringVar(&note, "note", "", i18n.S("等它是为了做什么：叫醒时带上这句", "what it's for: sent along when woken"))
	cmd.Flags().StringVar(&session, "session", "self", i18n.S("叫醒哪个会话（id / 标题；self = 你自己）", "which session to wake (id / title; self = you)"))

	var all bool
	list := &cobra.Command{
		Use:   "list",
		Short: i18n.S("你在等的（--all 看所有会话的）：每个的现在的状态、成不成立、插件接没接", "what you wait for (--all: every session's): the state now, whether it holds, whether the add-on took it"),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			c, err := newAPIClient()
			fail(err)
			q := ""
			if !all {
				id := c.selfID()
				if id == "" {
					fail(errors.New(i18n.S("认不出当前会话：不在 Gerdr 的 agent 里就用 --all", "cannot tell which session this is: outside a Gerdr agent use --all")))
				}
				q = "?session=" + url.QueryEscape(id)
			}
			var out struct {
				List []json.RawMessage `json:"list"`
			}
			fail(c.do("GET", "/api/waits"+q, nil, &out))
			printJSON(out.List)
		},
	}
	list.Flags().BoolVar(&all, "all", false, i18n.S("所有会话的", "every session's"))

	rm := &cobra.Command{
		Use:   "rm <wait id>",
		Short: i18n.S("不等了", "stop waiting"),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			c, err := newAPIClient()
			fail(err)
			fail(c.do("DELETE", "/api/waits/"+url.PathEscape(args[0]), nil, nil))
			fmt.Println("ok")
		},
	}

	state := &cobra.Command{
		Use:   "state <source> <key>",
		Short: i18n.S("一个东西最近报来的状态（有人在等它，插件才会去查）", "a thing's latest reported state (add-ons only look at what is waited on)"),
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			c, err := newAPIClient()
			fail(err)
			var out json.RawMessage
			fail(c.do("GET", "/api/states?source="+url.QueryEscape(args[0])+"&key="+url.QueryEscape(args[1]), nil, &out))
			printJSON(out)
		},
	}

	sources := &cobra.Command{
		Use:   "sources",
		Short: i18n.S("能等哪些东西：每种的 key 怎么写、有哪些字段、各是什么意思", "what can be waited on: how each kind's key is written, its fields and what they mean"),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			c, err := newAPIClient()
			fail(err)
			var out struct {
				List []json.RawMessage `json:"list"`
			}
			fail(c.do("GET", "/api/waits/sources", nil, &out))
			printJSON(out.List)
		},
	}
	cmd.AddCommand(list, rm, state, sources)
	return cmd
}
