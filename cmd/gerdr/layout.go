package main

// gerdr workspace / tab / pane …: let an agent see and rearrange the layout (sort tabs into workspaces, split panes together),
// shaped after herdr's commands of the same names. The work happens on the server (internal/server/layoutapi.go), scoped to the
// caller's herdr-style session; these commands only parse flags and print the JSON it returns.

import (
	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/server"
)

func layoutCall(method string, p server.LayoutParams) error {
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	var out any
	who := c.caller()
	if err := c.do("POST", "/api/layout/op", map[string]any{"method": method, "from": who.Session, "term": who.Term, "project": who.Project, "params": p}, &out); err != nil {
		return err
	}
	printJSON(out)
	return nil
}

// moveFlags: where `pane move` / `tab move` put things (herdr's pane move flags, plus left / up and --position).
func moveFlags(cmd *cobra.Command, p *server.LayoutParams, what string) {
	f := cmd.Flags()
	f.StringVar(&p.Tab, "tab", "", "split it into this tab (beside the whole tab, or beside --target-pane)")
	f.StringVar(&p.TargetPane, "target-pane", "", "split it beside this pane (pane id, or the conversation it shows)")
	f.StringVar(&p.Split, "split", "right", "with --tab / --target-pane: which side it goes on: right, down, left, up")
	f.Float64Var(&p.Ratio, "ratio", 0.5, "with --tab / --target-pane: its share of the split, 0.1–0.9")
	f.StringVar(&p.Workspace, "workspace", "", "make it a tab in this workspace (id, number or label)")
	f.BoolVar(&p.NewWorkspace, "new-workspace", false, "make it the first tab of a new workspace")
	f.StringVar(&p.Label, "label", "", "with --new-workspace: the workspace's label (default: the tab's)")
	f.IntVar(&p.Position, "position", 0, "with --workspace / --new-workspace: its place among the tabs / workspaces, 1 = first (default last)")
	if what == "pane" {
		f.BoolVar(&p.NewTab, "new-tab", false, "make it a tab of its own in the same workspace (or --workspace)")
		f.StringVar(&p.TabLabel, "tab-label", "", "when it becomes a tab of its own: the tab's label")
	}
}

func workspaceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "workspace", Short: "List / create / rename / reorder / close workspaces in gerdr (JSON, your session only)"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List workspaces, in sidebar order",
		RunE:  func(_ *cobra.Command, _ []string) error { return layoutCall("workspace.list", server.LayoutParams{}) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "get <workspace>",
		Short: "Show a workspace and its tabs (workspace = id, number or label)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			return layoutCall("workspace.get", server.LayoutParams{ID: a[0]})
		},
	})
	var cp server.LayoutParams
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a workspace (with one empty tab, dropped once something is moved in). Doesn't switch to it",
		RunE:  func(_ *cobra.Command, _ []string) error { return layoutCall("workspace.create", cp) },
	}
	create.Flags().StringVar(&cp.Label, "label", "", "its label")
	create.Flags().StringVar(&cp.Cwd, "cwd", "", "its folder (where new conversations in it start)")
	create.Flags().IntVar(&cp.Position, "position", 0, "its place in the sidebar, 1 = first (default last)")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use:   "rename <workspace> <label>",
		Short: "Rename a workspace",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			return layoutCall("workspace.rename", server.LayoutParams{ID: a[0], Label: a[1]})
		},
	})
	var mp server.LayoutParams
	move := &cobra.Command{
		Use:   "move <workspace> --position N",
		Short: "Move a workspace to another place in the sidebar",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			mp.ID = a[0]
			return layoutCall("workspace.move", mp)
		},
	}
	move.Flags().IntVar(&mp.Position, "position", 0, "1 = first")
	cmd.AddCommand(move)
	cmd.AddCommand(&cobra.Command{
		Use:   "close <workspace>",
		Short: "Remove an empty workspace (refuses one that still shows a conversation or a terminal)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			return layoutCall("workspace.close", server.LayoutParams{ID: a[0]})
		},
	})
	return cmd
}

func tabCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tab", Short: "List / create / rename / move / close tabs in gerdr (JSON, your session only)"}
	var lp server.LayoutParams
	list := &cobra.Command{
		Use:   "list",
		Short: "List tabs (split: row(a, b) = a left of b, col(a, b) = a above b)",
		RunE:  func(_ *cobra.Command, _ []string) error { return layoutCall("tab.list", lp) },
	}
	list.Flags().StringVar(&lp.Workspace, "workspace", "", "only this workspace's")
	cmd.AddCommand(list)
	cmd.AddCommand(&cobra.Command{
		Use:   "get <tab>",
		Short: "Show a tab and its panes (tab = id or label)",
		Args:  cobra.ExactArgs(1),
		RunE:  func(_ *cobra.Command, a []string) error { return layoutCall("tab.get", server.LayoutParams{ID: a[0]}) },
	})
	var cp server.LayoutParams
	create := &cobra.Command{
		Use:   "create",
		Short: "Create an empty tab (default: in your own workspace). Doesn't switch to it",
		RunE:  func(_ *cobra.Command, _ []string) error { return layoutCall("tab.create", cp) },
	}
	create.Flags().StringVar(&cp.Workspace, "workspace", "", "which workspace")
	create.Flags().StringVar(&cp.Label, "label", "", "its label")
	create.Flags().StringVar(&cp.Cwd, "cwd", "", "folder for the empty pane (default: the workspace's)")
	create.Flags().IntVar(&cp.Position, "position", 0, "its place among the tabs, 1 = first (default last)")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use:   "rename <tab> <label>",
		Short: `Rename a tab ("" = back to the default label, its folder)`,
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			return layoutCall("tab.rename", server.LayoutParams{ID: a[0], Label: a[1]})
		},
	})
	var mp server.LayoutParams
	move := &cobra.Command{
		Use:   "move <tab>",
		Short: "Move a whole tab (splits and all): to a workspace / place (--workspace, --position, --new-workspace), or merge it into another tab (--tab / --target-pane)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			mp.ID = a[0]
			return layoutCall("tab.move", mp)
		},
	}
	moveFlags(move, &mp, "tab")
	cmd.AddCommand(move)
	cmd.AddCommand(&cobra.Command{
		Use:   "close <tab>",
		Short: "Remove an empty tab (refuses one that still shows a conversation or a terminal)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			return layoutCall("tab.close", server.LayoutParams{ID: a[0]})
		},
	})
	return cmd
}

func paneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pane",
		Short: "List / inspect / move / swap panes in gerdr (JSON, your session only)",
		Long:  "A pane can be named by its pane id or by the conversation it shows (session id, 4+ character id prefix, or title).",
	}
	var lp server.LayoutParams
	list := &cobra.Command{
		Use:   "list",
		Short: "List panes: what each shows (agent conversation / terminal / empty), and where",
		RunE:  func(_ *cobra.Command, _ []string) error { return layoutCall("pane.list", lp) },
	}
	list.Flags().StringVar(&lp.Workspace, "workspace", "", "only this workspace's")
	cmd.AddCommand(list)
	cmd.AddCommand(&cobra.Command{
		Use:   "current",
		Short: "Your own pane",
		RunE:  func(_ *cobra.Command, _ []string) error { return layoutCall("pane.current", server.LayoutParams{}) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "get <pane>",
		Short: "Show a pane",
		Args:  cobra.ExactArgs(1),
		RunE:  func(_ *cobra.Command, a []string) error { return layoutCall("pane.get", server.LayoutParams{ID: a[0]}) },
	})
	var yp server.LayoutParams
	layout := &cobra.Command{
		Use:   "layout [pane]",
		Short: "How a tab is split: each pane's rect (fractions of the tab) and the splits (default: your own tab)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			if len(a) == 1 {
				yp.ID = a[0]
			}
			return layoutCall("pane.layout", yp)
		},
	}
	layout.Flags().StringVar(&yp.Tab, "tab", "", "this tab instead of a pane's")
	cmd.AddCommand(layout)
	var mp server.LayoutParams
	move := &cobra.Command{
		Use:   "move <pane>",
		Short: "Move a pane: split it into a tab (--tab / --target-pane, --split), or make it a tab of its own (--new-tab, --workspace, --new-workspace)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			mp.ID = a[0]
			return layoutCall("pane.move", mp)
		},
	}
	moveFlags(move, &mp, "pane")
	cmd.AddCommand(move)
	var sp server.LayoutParams
	swap := &cobra.Command{
		Use:   "swap <pane> --target-pane <pane>",
		Short: "Swap two panes' places (also across tabs)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			sp.ID = a[0]
			return layoutCall("pane.swap", sp)
		},
	}
	swap.Flags().StringVar(&sp.TargetPane, "target-pane", "", "the other pane")
	cmd.AddCommand(swap)
	return cmd
}
