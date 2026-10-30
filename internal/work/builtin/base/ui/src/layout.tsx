import { House } from "lucide-react";
import { NavLink, Outlet } from "react-router";
import clsx from "clsx";
import { project } from "./lib/gerdr";

// The menu on the left and the page on the right. This whole folder is yours: add pages in
// src/pages/ (file name = path) and links here; restyle anything.

const MENU = [
  {
    title: project,
    items: [{ to: "/", label: "首页", icon: House }],
  },
];

export default function Layout() {
  return (
    <div className="flex h-full">
      <nav className="flex w-56 shrink-0 flex-col gap-3 border-r bg-sidebar px-2 py-3">
        {MENU.map((g) => (
          <div key={g.title}>
            <div className="px-2 pb-1 text-[11px] font-medium text-muted-foreground">{g.title}</div>
            {g.items.map((it) => (
              <NavLink
                key={it.to}
                to={it.to}
                end
                className={({ isActive }) =>
                  clsx("flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px] hover:bg-accent/60", isActive && "bg-accent font-medium")
                }
              >
                <it.icon className="size-3.5 text-muted-foreground" />
                {it.label}
              </NavLink>
            ))}
          </div>
        ))}
      </nav>
      <main className="min-w-0 flex-1 overflow-y-auto">
        <Outlet />
      </main>
    </div>
  );
}
