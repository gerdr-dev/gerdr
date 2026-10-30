package native

import (
	"github.com/coder/acp-go-sdk"
)

// callerTool wraps an Options.Tools entry as a built-in style tool.
func callerTool(t Tool) *tool {
	params := t.Parameters
	if params == nil {
		params = obj(map[string]any{})
	}
	return &tool{
		name: t.Name, desc: t.Description, params: params, readOnly: t.ReadOnly,
		show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
			title := t.Name
			if t.Title != nil {
				if s := t.Title(args); s != "" {
					title = s
				}
			}
			return title, acp.ToolKindOther, nil
		},
		run: func(c *call, args map[string]any) result {
			s := c.r.s
			res, err := t.Run(c.ctx, ToolCall{
				SessionID: s.currentID(), ToolCallID: c.id, Cwd: s.cwd(), Meta: s.clientMetaMap(),
				Subagent: c.r.sub != nil, Args: args,
			})
			if err != nil {
				if c.ctx.Err() != nil {
					return result{} // the turn was cancelled: runTools records it as interrupted
				}
				return errResult("%v", err)
			}
			out := result{text: res.Text}
			if c.r.model.seesImages() {
				for _, img := range res.Images {
					out.images = append(out.images, s.a.imagePart(img.MIME, img.Data))
				}
			} else if len(res.Images) > 0 && out.text == "" {
				out.text = "(the tool returned images; the current model cannot view them)"
			}
			return out
		},
	}
}
