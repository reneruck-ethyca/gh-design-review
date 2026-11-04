# gh design-review

A GitHub CLI extension that renders the Markdown docs in a pull request, Mermaid diagrams included, and lets you review them on the rendered page. You can create, reply to, resolve, and submit PR review comments there, and they show up normally on GitHub's "Files changed" tab.

It runs locally on `127.0.0.1` and uses your existing `gh` auth. It needs no hosting and no OAuth app.

## Install

```sh
gh extension install reneruck/gh-design-review
```

## Usage

```sh
gh design-review              # PR for the current branch
gh design-review 123          # PR number
gh design-review <url|branch> # PR URL or branch name
```

| Flag | Default | |
|------|---------|-|
| `-R`, `--repo owner/repo` | current repo | Target repository |
| `--glob` | `docs/**/*.md` | Which changed files to list in the file tree |
| `--port` | random | Local server port |
| `--no-browser` | off | Print the URL instead of opening a browser |

In the page:

- The left-hand file tree lists every Markdown file the PR changes. Pick one to render it.
- A green margin bar marks blocks changed in the PR. A badge marks blocks that have threads.
- To comment, select text and press **Comment**, or click the **+** beside a block. You can add the comment to a pending review or post it right away.
- You can reply to and resolve threads in the side panel.
- **Submit review** sends the review as Comment, Approve, or Request changes.
- `Ctrl+C` stops the server.

## Development

Requires Go and Node 22.

```sh
make build   # build the frontend, then the gh-design-review binary
make test    # Go and frontend tests
make install   # build, then (re)install this checkout as `gh design-review`
```

To iterate on the frontend without rebuilding the binary, run `npm run dev` in `web/`, then start the binary with `DR_WEB_DIR=web/dist`.

Pushing a `v*` tag publishes precompiled binaries through `.github/workflows/release.yml`.
