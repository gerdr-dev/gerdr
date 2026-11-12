package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/artifacts"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// gerdr artifacts: the folder this conversation's artifacts go in, and its records (docs/dev/ARTIFACTS.md). Run by an
// agent inside Gerdr: the server works out which session it is (like `gerdr session`) and that session's folder.
func artifactsCmd() *cobra.Command {
	var dir, session string
	var asJSON bool
	cmd := &cobra.Command{
		Use: "artifacts",
		Short: i18n.S("产物：这个会话不进仓库、但人要看的东西放哪（打印文件夹），和要记下的链接 / 路径 / 文字",
			"Artifacts: where this conversation's output that isn't committed goes (prints the folder), and the links / paths / text to record"),
		Long: i18n.S(`不进仓库、但人要看或要留的东西（生成的图和视频、报告、草稿、导出的数据、要发布的文案）放进产物文件夹；代码和跟代码走的文档照常提交，不放这里。
文件夹第一次放东西时才建：默认是 ~/Gerdr/<项目>/<仓库>/，同一个仓库的所有会话共用。人在 Gerdr 里浏览、改其中的 Markdown。
不是文件的产物记一条：开的 PR / MR（link）、在仓库外建的东西（path）、一句要留下的结果（text）、置顶一个主要文件（pin）。记录只加不删，过时的用 --supersedes 换掉。`,
			`What the person needs to see or keep but the repository shouldn't hold (generated images and videos, reports, drafts, exported data, copy to publish) goes in the artifacts folder; code and the docs that belong with it are committed as usual, not put here.
The folder is created the first time something goes in: by default ~/Gerdr/<project>/<repo>/, shared by every session of that repository. The person browses it in Gerdr and edits its Markdown there.
What isn't a file gets a record: a PR / MR you opened (link), something created outside the repository (path), a short result worth keeping (text), the main file among many (pin). Records are never deleted; replace an outdated one with --supersedes.`),
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c := artifactsClient()
			out, err := c.artifactsGet(session, dir)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(out)
				return nil
			}
			fmt.Println(out.Dir)
			return nil
		},
	}
	cmd.PersistentFlags().StringVar(&dir, "dir", "", i18n.S("别的产物文件夹（绝对路径），不用这个会话的", "another artifacts folder (absolute path) instead of this session's"))
	cmd.PersistentFlags().StringVar(&session, "session", "", i18n.S("哪个会话的（默认：跑这条命令的 agent 自己）", "whose (default: the agent running this command)"))
	cmd.PersistentFlags().BoolVar(&asJSON, "json", false, i18n.S("输出 JSON", "print JSON"))

	cmd.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: i18n.S("列出记录和文件夹顶层的文件", "List the records and the files at the folder's top level"),
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c := artifactsClient()
			out, err := c.artifactsGet(session, dir)
			if err != nil {
				return err
			}
			var files struct {
				Entries []artifacts.Entry `json:"entries"`
			}
			if out.Exists {
				if err := c.do("GET", "/api/fs/list?path="+url.QueryEscape(out.Dir), nil, &files); err != nil {
					return err
				}
			}
			if asJSON {
				printJSON(map[string]any{"dir": out.Dir, "exists": out.Exists, "items": out.Items, "files": files.Entries})
				return nil
			}
			fmt.Println(out.Dir)
			if !out.Exists {
				fmt.Println(i18n.S("（还没有：第一次放东西时才建）", "(not there yet: created the first time something goes in)"))
				return nil
			}
			for _, it := range out.Items {
				fmt.Println(itemLine(it))
			}
			for _, e := range files.Entries {
				if e.Dir {
					fmt.Printf("  %s/\n", e.Name)
				} else {
					fmt.Printf("  %s  (%d B)\n", e.Name, e.Size)
				}
			}
			return nil
		},
	})

	var label, supersedes string
	record := func(use, short string, kind string, build func(arg string) (artifacts.Item, error)) *cobra.Command {
		c := &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				it, err := build(args[0])
				if err != nil {
					return err
				}
				it.Kind, it.Label, it.Supersedes = kind, label, supersedes
				c := artifactsClient()
				body := map[string]any{"dir": expandHome(dir), "session": session, "kind": it.Kind, "label": it.Label, "url": it.URL, "path": it.Path, "text": it.Text, "supersedes": it.Supersedes}
				if dir == "" && session == "" {
					id := c.selfID()
					if id == "" {
						return errors.New(i18n.S("不知道是哪个会话：在 Gerdr 里的 agent 里跑，或者给 --dir / --session", "Can't tell which session: run it from an agent in Gerdr, or give --dir / --session"))
					}
					body["session"] = id
				}
				var out struct {
					Dir  string         `json:"dir"`
					Item artifacts.Item `json:"item"`
				}
				if err := c.do("POST", "/api/artifacts", body, &out); err != nil {
					return err
				}
				if asJSON {
					printJSON(out)
					return nil
				}
				fmt.Printf("%s\n%s\n", out.Dir, itemLine(out.Item))
				return nil
			},
		}
		c.Flags().StringVar(&label, "label", "", i18n.S("一句说明（PR、新建的项目、测试账号…）", "a short label (PR, the new project, test account…)"))
		c.Flags().StringVar(&supersedes, "supersedes", "", i18n.S("这条换掉哪一条（它的 id，见 ls --json）", "the record this one replaces (its id, from ls --json)"))
		return c
	}
	cmd.AddCommand(
		record("link <url>", i18n.S("记一个链接：开的 PR / MR、流水线、发出去的帖子", "Record a link: a PR / MR you opened, a pipeline, a published post"), artifacts.KindLink,
			func(a string) (artifacts.Item, error) { return artifacts.Item{URL: a}, nil }),
		record("path <path>", i18n.S("记一个仓库外的位置：新建的项目、导出的目录", "Record a place outside the repository: a project you created, an export"), artifacts.KindPath,
			func(a string) (artifacts.Item, error) {
				p, err := filepath.Abs(expandHome(a))
				return artifacts.Item{Path: p}, err
			}),
		record("text <text>", i18n.S("记一句要留下的结果（长的写成 .md 放进文件夹再 pin）", "Record a short result worth keeping (longer: write a .md in the folder and pin it)"), artifacts.KindText,
			func(a string) (artifacts.Item, error) { return artifacts.Item{Text: a}, nil }),
		record("pin <file>", i18n.S("把文件夹里的一个文件置顶（主要的那个：成片、终稿）", "Pin a file in the folder (the main one: the final cut, the final draft)"), artifacts.KindFile,
			func(a string) (artifacts.Item, error) {
				p := expandHome(a)
				if !filepath.IsAbs(p) {
					if _, err := os.Stat(p); err == nil { // relative to where the command runs, when it is there
						p, _ = filepath.Abs(p)
					}
				}
				return artifacts.Item{Path: p}, nil
			}),
		record("retire <id>", i18n.S("把一条记录标成过时（不删，灰掉）", "Mark a record outdated (kept, greyed out)"), artifacts.KindRetire,
			func(a string) (artifacts.Item, error) {
				supersedes = a
				return artifacts.Item{}, nil
			}),
	)
	return cmd
}

type artifactsInfo struct {
	Dir    string           `json:"dir"`
	Exists bool             `json:"exists"`
	ID     string           `json:"id,omitempty"`
	Items  []artifacts.Item `json:"items"`
}

func artifactsClient() *apiClient {
	c, err := newAPIClient()
	if err != nil {
		fail(err)
	}
	return c
}

func (c *apiClient) artifactsGet(session, dir string) (artifactsInfo, error) {
	var out artifactsInfo
	q := url.Values{}
	switch {
	case dir != "":
		q.Set("dir", expandHome(dir))
	case session != "":
		q.Set("session", session)
	default:
		id := c.selfID()
		if id == "" {
			return out, errors.New(i18n.S("不知道是哪个会话：在 Gerdr 里的 agent 里跑，或者给 --dir / --session", "Can't tell which session: run it from an agent in Gerdr, or give --dir / --session"))
		}
		q.Set("session", id)
	}
	err := c.do("GET", "/api/artifacts?"+q.Encode(), nil, &out)
	return out, err
}

func itemLine(it artifacts.Item) string {
	what := it.URL + it.Path + it.Text
	if it.Label != "" {
		what = it.Label + ": " + what
	}
	old := ""
	if it.Superseded {
		old = i18n.S("（已过时）", " (outdated)")
	}
	return fmt.Sprintf("- [%s] %s  %s%s  #%s", it.Kind, strings.ReplaceAll(what, "\n", " "), time.UnixMilli(it.At).Format("01-02 15:04"), old, it.ID)
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}
