// Every "action": shortcuts, the command palette, sidebar menus and add-ons all call these instead of writing their own.
// The code lives in actions/, one file per area, so agents editing different areas don't collide in one big file;
// this file only re-exports them, so importers keep using "@/lib/actions". actions/shared.ts holds helpers the areas
// share and is not re-exported (apart from basename).
export { basename } from "./actions/shared";
export * from "./actions/panes";
export * from "./actions/tabs";
export * from "./actions/nav";
export * from "./actions/reopen";
export * from "./actions/workspaces";
export * from "./actions/spaces";
export * from "./actions/terminal";
export * from "./actions/sessions";
export * from "./actions/runAction";
export * from "./actions/files";
