---
name: gerdr-events
description: "Things outside the conversation, from inside Gerdr: be told when something happens (a chat message, an MR comment, a pipeline: `gerdr event watch / list`) instead of polling or sleeping; wait until something is in a state (an MR got review comments, its pipeline failed, it merged: `gerdr wait`), e.g. right after opening an MR. Also artifacts (`gerdr artifacts`) in a task a panel (board) started: where output that isn't committed goes (images, videos, reports, drafts, copy to publish) and recording a PR / MR link, a folder or a short result so the person finds it later. Requires GERDR_ENV=1."
---

# Gerdr: events, waiting for a state, artifacts

Only inside Gerdr (the GUI you run in). Check first; if not, say so and stop:

```bash
test "${GERDR_ENV:-}" = 1
```

Commands print JSON; an error is one `{"error": "..."}` line on stderr and exit code 1. `--help` on any command has the
installed version's exact flags.

## Being told when something happens (events)

Gerdr keeps **events** from outside the conversation — MR comments, pipelines, chat messages, whatever a panel's poller
or an add-on reports — as `{id, source, kind, key, title, text, url}`. Instead of polling or sleeping, subscribe and end
your turn; each matching event arrives as a message to you:

```bash
gerdr event list --since 6h                    # what has been coming in (source/kind/key to subscribe by)
gerdr event watch --on 'gitlab/mr.note' --key 'group/repo!34' --note "fix the review comments and push"
gerdr event watch --on 'ci/pipeline.*' --key 'group/repo' --once   # just the next one
gerdr event watches                            # yours; gerdr event unwatch <id>
```

`--on` is `source/kind` with `*`; `--key` narrows to one thing (`*` crosses slashes). `--note` comes back with every
event, so you remember what you meant to do. Only events someone emits arrive, and only from the moment you watch:
an event is said once. When nothing at all emits that kind, `watch` fails instead of waiting forever.

## Waiting for a state (an MR, a pipeline): `gerdr wait`

For things that **are** in some state — an MR (open / merged, its pipeline, its review comments), whatever an add-on
reports — don't watch events: wait until a condition on the state holds. It is checked against the state as it is,
so waiting late (you forgot, the user reminds you five minutes later) still wakes at once with what is there.

```bash
gerdr wait sources                              # what can be waited on, how keys look, the fields
gerdr wait gitlab 'gitlab.example.com/group/repo!34' \
  --until "state != 'opened' || pipeline == 'failed' || reviews > 0 || need_rebase" \
  --note "handle review comments and failed pipelines; clean up once merged"
gerdr wait list                                 # yours, with the state now; gerdr wait rm <id>
gerdr wait state gitlab 'gitlab.example.com/group/repo!34'   # the latest state, to write the next condition from
```

- The key is as the add-on names the thing; a GitLab MR is the host, the project path and `!iid`, as in its address.
- `--until` is one JavaScript expression over the state: each field is a variable (`$` is the whole state), strings
  in quotes. Lists filter like any JS: `notes.some(n => n.author != me && n.resolvable && !n.resolved)` (an
  unresolved comment by someone else). It reads only the state, 50ms per check; a misspelt field is refused.
- Woken once, then the wait is over. The message has the state's values now: to keep going, wait again with your
  next expectation built from them. Write counts as "more than now" (`reviews > 3` when there are 3) — `reviews > 0`
  again right after handling them would wake you at once. Ending your turn after `gerdr wait` is right: the message
  wakes you.
- A wait belongs to this conversation: closing it, deleting it or `/clear` drops it (a Gerdr restart doesn't).
- After opening an MR: wait on it straight away, in the same turn. Needs the GitLab add-on (`gerdr addon install
  gitlab`) and `glab auth login --hostname <host>`; the output says if the add-on can't take the key and why.

## Artifacts: output that isn't committed (`gerdr artifacts`)

Only a task conversation a panel (a board) started has an artifacts folder, one per task: your system prompt names it
("Artifacts folder"). Without that section you have none (plain Code conversations don't): put things where the person
asked, and don't run these commands. What the person needs to see or keep but the repository
shouldn't hold — generated images and videos, screenshots, reports, drafts, exported data, copy to publish — goes
there, a subfolder per piece of work. Code, and docs that belong with the code, are committed as usual instead. Most
coding work has no artifacts: don't create the folder without a reason. The person browses it on the panel's task and edits its Markdown there, so read a file again before changing it.

```bash
gerdr artifacts                                   # the folder (created the first time something goes in)
gerdr artifacts ls                                # its records and top-level files; --json for ids
gerdr artifacts link https://gitlab.example.com/g/r/-/merge_requests/34 --label MR
gerdr artifacts path ~/dev/new-demo --label "the demo project"
gerdr artifacts text "test account: demo / demo123" --label account
gerdr artifacts pin "首页配图/final.png"            # the main file among many
gerdr artifacts link <new url> --supersedes <id>  # replaces an outdated record; `retire <id>` just marks it
```

- Records are kept, never deleted: an outdated one is replaced (`--supersedes`) and drawn greyed.
- Longer text than a line or two goes in a `.md` file in the folder (then `pin` it), not in a text record.
- Scratch files go in the folder's `草稿/` (or `drafts/`): `.gerdrignore` keeps them out of syncing.

