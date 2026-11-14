package main

import (
	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// hostCmd: the other computers this one controls (docs/dev/MULTIHOST.md), and doing things on them directly: list
// them, run a command there (exec, the ssh of it), copy files there and back (cp, the scp of it). One group so an
// agent finds all three from `gerdr host --help`. Sessions on them stay under `gerdr session` (`new --host`).
func hostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host",
		Short: i18n.S("这台电脑控制的其他电脑：列出（list）、在上面跑命令（exec）、来回拷文件 / 目录（cp）", "Other computers this one controls: list them, run a command there (exec), copy files / folders there and back (cp)"),
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: i18n.S("这台电脑控制的其他电脑（id 给 exec / cp / session new --host 用）：它们上面的会话在 session list 里带 host", "Other computers this one controls (their id goes to exec / cp / session new --host); their sessions show in session list with a host"),
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			var out []map[string]any
			if err := c.do("GET", "/api/hosts", nil, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	})
	cmd.AddCommand(hostExecCmd(), hostCpCmd())
	return cmd
}
