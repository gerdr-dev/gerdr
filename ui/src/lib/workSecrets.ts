/**
 * A page asking for a key (core askSecret, or a Work site's gerdr.ts secret()): gerdr's own password dialog
 * (WorkView's SecretPrompt) takes it and saves it as a Work secret; the value never passes through the page, code an
 * agent wrote. One at a time: a second ask while one is open waits for it.
 */
export type SecretAsk = { name: string; label: string; note: string; done: (saved: boolean, error?: string) => void };

let queue: SecretAsk[] = [];
const subs = new Set<() => void>();
const notify = () => subs.forEach((f) => f());

export function askSecret(name: string, opts: { label?: string; note?: string } = {}): Promise<boolean> {
  return new Promise((resolve, reject) => {
    queue = [
      ...queue,
      {
        name,
        label: opts.label ?? "",
        note: opts.note ?? "",
        done: (saved, error) => {
          queue = queue.slice(1);
          notify();
          if (error) reject(new Error(error));
          else resolve(saved);
        },
      },
    ];
    notify();
  });
}

export const secretAsks = {
  current: () => queue[0] ?? null,
  subscribe: (f: () => void) => (subs.add(f), () => void subs.delete(f)),
};
