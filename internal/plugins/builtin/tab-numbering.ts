/// <reference path="./gerdr.d.ts" />
// 给每个 tab 加上位置编号（"1.claude"、"2.web"），prefix+1..9 跳哪个一眼就知道。
// 同名的 tab 编成一组，按位置加罗马数字后缀（"web"、"web Ⅱ"），和 herdr-diy 的 tab-numbering 一样。
const ROMAN = ["", " Ⅱ", " Ⅲ", " Ⅳ", " Ⅴ", " Ⅵ", " Ⅶ", " Ⅷ", " Ⅸ", " Ⅹ", " Ⅺ", " Ⅻ"];

export default definePlugin({
  name: "tab-numbering",
  setup(g) {
    return g.tabLabel((tab, ctx) => {
      const same = ctx.workspace.tabs.filter((t) => t.name === tab.name);
      const nth = same.findIndex((t) => t.id === tab.id);
      const suffix = same.length > 1 ? (ROMAN[nth] ?? ` (${nth + 1})`) : "";
      return `${ctx.index + 1}.${ctx.label}${suffix}`;
    });
  },
});
