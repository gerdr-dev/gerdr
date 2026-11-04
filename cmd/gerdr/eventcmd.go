package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// gerdr event: the event model from the command line (docs/dev/EVENTS.md). Emitting is for scripts and sources
// without an add-on; list / watch / unwatch / watches are what an agent uses to be told when something happens.
func eventCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "event",
		Short: i18n.S("事件：发出、查看、订阅（MR 新评论、流水线、群消息……）", "Events: emit, list, watch (MR comments, pipelines, chat messages…)"),
		Long: i18n.S(`事件是外面发生的事（MR 有新评论、流水线挂了、群里有人 @ 你），统一成一种记录：
  {id, source, kind, key, actor, title, text, url, data}
source/kind 例如 gitlab/mr.note、ci/pipeline.failed、slack/message；key 是它说的是哪个东西（group/repo!34、频道 id）。

订阅：会话用 gerdr event watch（事件来了发一条消息给它）；面板 / Work 写 triggers/*.json；插件在 addon.json 里声明。
模式写 source/kind，可以用 *：gitlab/mr.*、*/pipeline.failed；只写 kind 就是不管来源。`,
			`An event is something that happened outside (a new MR comment, a failed pipeline, a mention in a chat), as one record:
  {id, source, kind, key, actor, title, text, url, data}
source/kind e.g. gitlab/mr.note, ci/pipeline.failed, slack/message; key is what it is about (group/repo!34, a channel id).

Subscribing: a session uses gerdr event watch (each event comes as a message); a panel / Work writes triggers/*.json;
an add-on declares it in addon.json. Patterns are source/kind with * globs: gitlab/mr.*, */pipeline.failed; a kind
alone matches any source.`),
	}

	var lOn, lKey, lSince string
	var lLimit int
	list := &cobra.Command{
		Use:   "list",
		Short: i18n.S("最近的事件（默认 24 小时内、最新 100 条）", "recent events (default: the last 24 hours, newest 100)"),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			c, err := newAPIClient()
			fail(err)
			q := url.Values{}
			if lOn != "" {
				q.Set("on", lOn)
			}
			if lKey != "" {
				q.Set("key", lKey)
			}
			if lSince != "" {
				d, err := time.ParseDuration(lSince)
				if err != nil {
					fail(fmt.Errorf("--since: %w", err))
				}
				q.Set("since", strconv.FormatInt(time.Now().Add(-d).UnixMilli(), 10))
			}
			if lLimit > 0 {
				q.Set("limit", strconv.Itoa(lLimit))
			}
			var out struct {
				List []json.RawMessage `json:"list"`
			}
			fail(c.do("GET", "/api/events?"+q.Encode(), nil, &out))
			printJSON(out.List)
		},
	}
	list.Flags().StringVar(&lOn, "on", "", i18n.S("只看这类：source/kind，可以用 *", "only these: source/kind, * allowed"))
	list.Flags().StringVar(&lKey, "key", "", i18n.S("只看说这个东西的（可以用 *）", "only events about this key (* allowed)"))
	list.Flags().StringVar(&lSince, "since", "", i18n.S("多久以内，如 30m、6h、72h", "how far back, e.g. 30m, 6h, 72h"))
	list.Flags().IntVarP(&lLimit, "limit", "n", 0, i18n.S("最多几条（最新的）", "at most this many (the newest)"))

	emit := &cobra.Command{
		Use:   "emit <json>",
		Short: i18n.S(`发出一个事件：'{"source":"ci","kind":"pipeline.failed","key":"g/r","title":"…","id":"可选，用来去重"}'`, `emit an event: '{"source":"ci","kind":"pipeline.failed","key":"g/r","title":"…","id":"optional, for dedup"}'`),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			c, err := newAPIClient()
			fail(err)
			var body map[string]any
			if err := json.Unmarshal([]byte(args[0]), &body); err != nil {
				fail(fmt.Errorf(i18n.S("不是 JSON 对象：%w", "not a JSON object: %w"), err))
			}
			var out map[string]any
			fail(c.do("POST", "/api/events", body, &out))
			printJSON(out)
		},
	}

	var wOn, wKey, wSession, wNote string
	var wOnce bool
	watch := &cobra.Command{
		Use:   "watch",
		Short: i18n.S("订阅：匹配的事件来了就发一条消息给这个会话（默认你自己）", "watch: each matching event is sent to this session as a message (default: you)"),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			c, err := newAPIClient()
			fail(err)
			if wOn == "" {
				fail(errors.New(i18n.S("要给 --on：哪类事件（source/kind，可以用 *）", "--on is required: which events (source/kind, * allowed)")))
			}
			target, err := c.reportTarget(wSession)
			fail(err)
			var out map[string]any
			fail(c.do("POST", "/api/events/watches", map[string]any{"on": wOn, "key": wKey, "session": target, "once": wOnce, "note": wNote}, &out))
			printJSON(out)
		},
	}
	watch.Flags().StringVar(&wOn, "on", "", i18n.S("哪类事件：source/kind，可以用 *（gitlab/mr.*）", "which events: source/kind, * allowed (gitlab/mr.*)"))
	watch.Flags().StringVar(&wKey, "key", "", i18n.S("只要说这个东西的（group/repo!34，可以用 *）", "only events about this (group/repo!34, * allowed)"))
	watch.Flags().StringVar(&wSession, "session", "self", i18n.S("发给哪个会话（id / 标题；self = 你自己）", "which session gets them (id / title; self = you)"))
	watch.Flags().BoolVar(&wOnce, "once", false, i18n.S("只要第一条，之后自动取消", "only the first one, then it's removed"))
	watch.Flags().StringVar(&wNote, "note", "", i18n.S("订阅是为了做什么：每条事件都会带上这句，提醒你收到后干嘛", "what it's for: sent along with each event, to remind you what to do"))

	unwatch := &cobra.Command{
		Use:   "unwatch <watch id>",
		Short: i18n.S("取消一个订阅", "remove a watch"),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			c, err := newAPIClient()
			fail(err)
			fail(c.do("DELETE", "/api/events/watches/"+url.PathEscape(args[0]), nil, nil))
			fmt.Println("ok")
		},
	}

	var wsAll bool
	watches := &cobra.Command{
		Use:   "watches",
		Short: i18n.S("你的订阅（--all 看所有会话的）", "your watches (--all: every session's)"),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			c, err := newAPIClient()
			fail(err)
			q := ""
			if !wsAll {
				id := c.selfID()
				if id == "" {
					fail(errors.New(i18n.S("认不出当前会话：不在 Gerdr 的 agent 里就用 --all", "cannot tell which session this is: outside a Gerdr agent use --all")))
				}
				q = "?session=" + url.QueryEscape(id)
			}
			var out struct {
				List []json.RawMessage `json:"list"`
			}
			fail(c.do("GET", "/api/events/watches"+q, nil, &out))
			printJSON(out.List)
		},
	}
	watches.Flags().BoolVar(&wsAll, "all", false, i18n.S("所有会话的订阅", "every session's watches"))

	cmd.AddCommand(list, emit, watch, unwatch, watches)
	return cmd
}
