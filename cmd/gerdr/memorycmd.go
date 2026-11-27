package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/repomem"
)

// gerdr memory: the person's notes per repository (internal/repomem). Files only, no server: an agent runs it in its
// own directory, and the repository is the one that directory belongs to (a worktree's is its main repository).
func memoryCmd() *cobra.Command {
	var repo string
	var asJSON bool
	which := func() (string, error) {
		d := repo
		if d == "" {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			d = wd
		}
		if strings.HasPrefix(d, "~/") {
			if h, err := os.UserHomeDir(); err == nil {
				d = filepath.Join(h, d[2:])
			}
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			return "", err
		}
		return repomem.Root(abs), nil
	}
	root := func() string { return config.FromEnv().Dir }
	cmd := &cobra.Command{
		Use: "memory",
		Short: i18n.S("仓库记忆：人对一个仓库交代过的规矩（走不走 MR、谁来合、发布找谁批），每个在这个仓库干活的会话都会带上",
			"Repository memory: what the person told agents about a repository (MR or not, who merges, who approves a release); every session working there gets it"),
		Long: i18n.S(`仓库记忆按仓库路径存一份，所有 Gerdr 项目、所有 agent 共用；在这个仓库（或它的 worktree）里开的每个会话，系统提示里都有这份全文，别的有记忆的仓库列出文件路径。
只记人说过或确认过、仓库里没有的事；对所有仓库都成立的写进项目记忆。不带子命令：打印当前目录所属仓库的记忆和文件路径。`,
			`Kept per repository path, shared by every Gerdr project and agent; every session started in the repository (or a worktree of it) has the notes whole in its system prompt, and the other repositories with notes listed by file.
Only what the person said or confirmed and the repository doesn't record; what holds for all repositories goes in the project memory. Without a subcommand: prints the notes of the repository the current directory belongs to, and their file.`),
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			r, err := which()
			if err != nil {
				return err
			}
			n := repomem.Read(root(), r)
			if asJSON {
				printJSON(n)
				return nil
			}
			fmt.Printf("%s\n%s\n\n", n.Repo, n.Path)
			if n.Text == "" {
				fmt.Println(i18n.S("（还没有记忆）", "(no notes yet)"))
			} else {
				fmt.Println(n.Text)
			}
			return nil
		},
	}
	cmd.PersistentFlags().StringVar(&repo, "repo", "", i18n.S("哪个仓库（路径；默认：当前目录所属的仓库）", "which repository (a path; default: the one the current directory belongs to)"))
	cmd.PersistentFlags().BoolVar(&asJSON, "json", false, i18n.S("输出 JSON", "print JSON"))

	cmd.AddCommand(&cobra.Command{
		Use:   "add <fact>",
		Short: i18n.S("记一条（一句话，人说过或确认过的；已经记着的不重复记）", "Add a note (one sentence the person said or confirmed; one already there isn't added again)"),
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			r, err := which()
			if err != nil {
				return err
			}
			fact := strings.Join(strings.Fields(strings.Join(args, " ")), " ")
			if fact == "" {
				return errors.New(i18n.S("要记的内容是空的", "nothing to add"))
			}
			n, added, err := repomem.Add(root(), r, fact+"（"+time.Now().Format("2006-01-02")+"）")
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(map[string]any{"repo": n.Repo, "path": n.Path, "added": added})
				return nil
			}
			if !added {
				fmt.Println(i18n.S("已经记着这一条了：", "Already noted: ") + n.Path)
				return nil
			}
			fmt.Println(i18n.S("记下了：", "Noted: ") + n.Path)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: i18n.S("列出有记忆的仓库和文件", "List the repositories with notes, and their files"),
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			l := repomem.List(root())
			if asJSON {
				printJSON(l)
				return nil
			}
			for _, n := range l {
				fmt.Printf("%s\t%d\t%s\n", n.Repo, strings.Count(n.Text, "\n")+1, n.Path)
			}
			return nil
		},
	})
	return cmd
}
