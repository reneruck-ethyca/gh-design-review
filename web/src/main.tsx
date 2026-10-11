import { render } from "preact";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks";
import DOMPurify from "dompurify";
import "github-markdown-css/github-markdown.css";
import "./style.css";
import { api, ApiError, setRenderedHead, type DocFile, type Mode, type PR, type Review, type ReviewEvent, type Thread } from "./api";
import { blocks, markChanged, renderMarkdown, renderMermaid, rewriteLinks } from "./render";
import { blockForLine, blocksInRange, buildComment, placeThread, selectionRange, type Placement, type Range } from "./anchor";

type Composer = { range: Range; quote: string; below: number }; // below: px from doc-col top
type Placed = { t: Thread; p: Placement; block?: HTMLElement };

function App() {
  const [pr, setPr] = useState<PR | null>(null);
  const [path, setPath] = useState("");
  const [file, setFile] = useState<DocFile | null>(null);
  const [threads, setThreads] = useState<Thread[]>([]);
  const [review, setReview] = useState<Review>(null);
  const [banner, setBanner] = useState("");
  const [toast, setToast] = useState("");
  const [showChanges, setShowChanges] = useState(true);
  const [composer, setComposer] = useState<Composer | null>(null);
  const [hover, setHover] = useState<HTMLElement | null>(null);
  const [sel, setSel] = useState<(Composer & { top: number; left: number }) | null>(null);
  const [layout, setLayout] = useState(0); // bumped to re-measure gutter positions
  const mainRef = useRef<HTMLElement>(null);
  const docRef = useRef<HTMLElement>(null);
  const dialogRef = useRef<HTMLDialogElement>(null);
  const [reviewBody, setReviewBody] = useState("");

  const fail = (e: unknown) => {
    if (e instanceof ApiError && (e.code === "stale_head" || e.code === "server_down")) setBanner(e.message);
    else setToast(e instanceof Error ? e.message : String(e));
  };

  useEffect(() => {
    api.pr().then((p) => {
      setPr(p);
      const fromHash = decodeURIComponent(location.hash.slice(1));
      setPath(p.docs.some((d) => d.path === fromHash) ? fromHash : p.docs[0]?.path ?? "");
    }).catch(fail);
  }, []);

  const reload = (p = path) =>
    Promise.all([api.threads(p).then(setThreads), api.review().then(setReview)]).catch(fail);

  useEffect(() => {
    if (!path) return;
    location.hash = encodeURIComponent(path);
    setFile(null);
    setComposer(null);
    setThreads([]);
    api.file(path).then((f) => { setRenderedHead(f.headSha); setFile(f); }).catch(fail);
    reload(path);
  }, [path]);

  useLayoutEffect(() => {
    const root = docRef.current;
    if (!root || !file || !pr) return;
    root.innerHTML = renderMarkdown(file.content);
    rewriteLinks(root, file.path, `${new URL(pr.url).origin}/${pr.headOwner}/${pr.headRepo}/blob/${file.headSha}`);
    // In an added file every block is new, so bars would carry no information.
    if (file.status !== "added") markChanged(root, file.addedLines);
    setLayout((l) => l + 1);
    renderMermaid(root).then(() => setLayout((l) => l + 1));
  }, [file]);

  useEffect(() => {
    const ro = new ResizeObserver(() => setLayout((l) => l + 1));
    ro.observe(docRef.current!);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    if (!file) return;
    const id = setInterval(() => api.pr().then((p) => {
      if (p.headSha !== file.headSha) setBanner("New commits were pushed. Reload to see them.");
    }).catch(fail), 60_000);
    return () => clearInterval(id);
  }, [file]);

  useEffect(() => {
    if (!toast) return;
    const id = setTimeout(() => setToast(""), 8000);
    return () => clearTimeout(id);
  }, [toast]);

  const placed: Placed[] = useMemo(() => {
    const bs = docRef.current ? blocks(docRef.current) : [];
    return threads.map((t) => {
      const p = placeThread(t, file?.headSha ?? "");
      return { t, p, block: p.section === "doc" ? blockForLine(bs, p.line)?.el : undefined };
    });
  }, [threads, file, layout]);

  // Preact 11 no longer appends "px" to numeric styles.
  const topOf = (el: Element) => `${el.getBoundingClientRect().top - mainRef.current!.getBoundingClientRect().top}px`;

  const highlight = (r: Range | null) => {
    const root = docRef.current!;
    root.querySelectorAll(".hl").forEach((e) => e.classList.remove("hl"));
    if (r) blocksInRange(blocks(root), r.start, r.end).forEach((b) => b.el.classList.add("hl"));
  };
  const jump = (line: number) => blockForLine(blocks(docRef.current!), line)?.el.scrollIntoView({ block: "center", behavior: "smooth" });
  const focusThread = (id: string) => {
    const el = document.getElementById("thread-" + id) as HTMLDetailsElement | null;
    if (!el) return;
    el.open = true;
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    el.classList.remove("flash");
    void el.offsetWidth;
    el.classList.add("flash");
  };

  const onMouseUp = () => requestAnimationFrame(() => {
    const s = getSelection();
    const range = s && selectionRange(s, docRef.current!);
    if (!s || !range) return setSel(null);
    const rect = s.getRangeAt(0).getBoundingClientRect();
    const box = mainRef.current!.getBoundingClientRect();
    setSel({ range, quote: s.toString(), top: rect.top - box.top - 36, left: rect.left - box.left, below: rect.bottom - box.top + 8 });
  });
  const openComposer = (c: Composer) => {
    setComposer(c);
    setSel(null);
    getSelection()?.removeAllRanges();
  };

  const badges = new Map<HTMLElement, Thread[]>();
  for (const x of placed) if (x.block) badges.set(x.block, [...(badges.get(x.block) ?? []), x.t]);

  const submit = async (event: ReviewEvent) => {
    try {
      await api.submit(event, reviewBody);
      dialogRef.current!.close();
      setReviewBody("");
      reload();
    } catch (e) { fail(e); }
  };
  const discard = async () => {
    if (!confirm("Discard your pending review and all its comments?")) return;
    try {
      await api.discard();
      dialogRef.current!.close();
      reload();
    } catch (e) { fail(e); }
  };

  return (
    <>
      <header class="topbar">
        {pr && <a class="title" href={pr.url} target="_blank" rel="noopener noreferrer">#{pr.number} {pr.title}</a>}
        {pr && pr.docs.length > 1 && (
          <select value={path} onChange={(e) => setPath(e.currentTarget.value)}>
            {pr.docs.map((d) => <option value={d.path}>{d.path}</option>)}
          </select>
        )}
        {pr && pr.docs.length === 1 && <code>{path}</code>}
        <span class="spacer" />
        <label><input type="checkbox" checked={showChanges} onChange={(e) => setShowChanges(e.currentTarget.checked)} /> Show changes</label>
        <button onClick={() => reload()}>Refresh</button>
        <button class="primary" onClick={() => dialogRef.current!.showModal()}>
          {review ? `Pending: ${review.count} · ` : ""}Submit review
        </button>
      </header>
      {banner && <div class="banner">{banner} <button onClick={() => location.reload()}>Reload</button></div>}
      <div class="layout">
        <main ref={mainRef} class="doc-col" onMouseUp={onMouseUp} onMouseLeave={() => setHover(null)}
          onMouseOver={(e) => {
            const b = (e.target as Element).closest<HTMLElement>("[data-line-start]");
            if (b && docRef.current!.contains(b)) setHover(b);
          }}>
          {!file && <div class="skeleton">{[90, 70, 80, 60, 75].map((w) => <div style={{ width: w + "%" }} />)}</div>}
          <article ref={docRef} class={`markdown-body ${showChanges ? "show-changes" : ""}`} />
          {file && hover && (
            <button class="plus" title="Comment on this block" style={{ top: topOf(hover) }}
              onClick={() => openComposer({ range: { start: +hover.dataset.lineStart!, end: +hover.dataset.lineEnd! }, quote: hover.textContent ?? "",
                below: hover.getBoundingClientRect().bottom - mainRef.current!.getBoundingClientRect().top + 8 })}>+</button>
          )}
          {file && [...badges].map(([el, ts]) => (
            <button class="badge" style={{ top: topOf(el) }} onClick={() => focusThread(ts[0].id)}
              onMouseEnter={() => highlight({ start: +el.dataset.lineStart!, end: +el.dataset.lineEnd! })} onMouseLeave={() => highlight(null)}>
              💬 {ts.length}
            </button>
          ))}
          {sel && (
            <button class="sel-btn primary" style={{ top: `${sel.top}px`, left: `${sel.left}px` }}
              onMouseDown={(e) => e.preventDefault()} onClick={() => openComposer(sel)}>Comment</button>
          )}
          {file && composer && (
            <ComposerBox c={composer} file={file} fail={fail}
              onDone={() => { setComposer(null); reload(); }} onCancel={() => setComposer(null)} />
          )}
        </main>
        <aside class="panel">
          {file && <Panel placed={placed} reload={reload} fail={fail} highlight={highlight} jump={jump} />}
        </aside>
      </div>
      <dialog ref={dialogRef} class="submit-dialog">
        <h3>Submit review</h3>
        <textarea placeholder="Leave a comment (optional)" value={reviewBody} onInput={(e) => setReviewBody(e.currentTarget.value)} />
        <div class="row">
          <button class="primary" onClick={() => submit("COMMENT")}>Comment</button>
          <button onClick={() => submit("APPROVE")}>Approve</button>
          <button onClick={() => submit("REQUEST_CHANGES")}>Request changes</button>
          <span class="spacer" />
          <button class="link" onClick={() => dialogRef.current!.close()}>Cancel</button>
        </div>
        {review && <button class="link danger" onClick={discard}>Discard pending review ({review.count})</button>}
      </dialog>
      {toast && <div class="toast" onClick={() => setToast("")}>{toast}</div>}
    </>
  );
}

type PanelProps = {
  placed: Placed[];
  reload: () => void; fail: (e: unknown) => void; highlight: (r: Range | null) => void; jump: (line: number) => void;
};

function Panel({ placed, reload, fail, highlight, jump }: PanelProps) {
  const general = placed.filter((x) => x.p.section === "general");
  const outdated = placed.filter((x) => x.p.section === "outdated");
  type Item = { line: number; node: preact.JSX.Element };
  const items: Item[] = placed.flatMap((x) => x.p.section === "doc"
    ? [{ line: x.p.start, node: <ThreadView key={x.t.id} {...x} reload={reload} fail={fail} highlight={highlight} jump={jump} /> }]
    : []);
  items.sort((a, b) => a.line - b.line);
  const view = (x: Placed) => <ThreadView key={x.t.id} {...x} reload={reload} fail={fail} highlight={highlight} jump={jump} />;

  return (
    <>
      {general.length > 0 && <section><h4>General comments</h4>{general.map(view)}</section>}
      <section>
        <h4>In this document</h4>
        {items.length ? items.map((i) => i.node) : <p class="muted">No comments yet. Select text or click + beside a block.</p>}
      </section>
      {outdated.length > 0 && <section><h4>Outdated</h4>{outdated.map(view)}</section>}
    </>
  );
}

function ComposerBox({ c, file, fail, onDone, onCancel }: {
  c: Composer; file: DocFile; fail: (e: unknown) => void; onDone: () => void; onCancel: () => void;
}) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const fileLevel = buildComment(c.range, c.quote, "", file.hunks, file.headSha).subjectType === "FILE";
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.querySelector("textarea")?.focus({ preventScroll: true });
    ref.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [c]);

  const post = async (mode: Mode) => {
    if (!text.trim() || busy) return;
    setBusy(true);
    try {
      await api.createThread({ path: file.path, mode, ...buildComment(c.range, c.quote, text, file.hunks, file.headSha) });
      onDone();
    } catch (e) {
      fail(e); // draft text stays in the textarea
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="composer floating" ref={ref} style={{ top: `${c.below}px` }}>
      <div class="muted">Lines {c.range.start}{c.range.end !== c.range.start && `–${c.range.end}`}</div>
      {c.quote.trim() && <blockquote>{c.quote.trim().slice(0, 300)}</blockquote>}
      {fileLevel && <div class="note">Outside the diff. This will be posted as a file comment.</div>}
      <textarea value={text} placeholder="Leave a comment (Markdown)" onInput={(e) => setText(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") onCancel();
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); post(e.shiftKey ? "now" : "review"); }
        }} />
      <div class="row">
        <button class="primary" disabled={busy} onClick={() => post("review")} title="Cmd/Ctrl+Enter">Add to review</button>
        <button disabled={busy} onClick={() => post("now")} title="Shift+Cmd/Ctrl+Enter">Comment now</button>
        <span class="spacer" />
        <button class="link" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

function ThreadView({ t, p, reload, fail, highlight, jump }: Placed & {
  reload: () => void; fail: (e: unknown) => void; highlight: (r: Range | null) => void; jump: (line: number) => void;
}) {
  const [reply, setReply] = useState("");
  const [busy, setBusy] = useState(false);
  const run = async (fn: () => Promise<unknown>, after?: () => void) => {
    setBusy(true);
    try { await fn(); after?.(); reload(); } catch (e) { fail(e); } finally { setBusy(false); }
  };
  const send = (mode: Mode) => reply.trim() && run(() => api.reply(t, reply, mode), () => setReply(""));
  const pendingThread = t.comments.every((c) => c.state === "PENDING");

  let label = "File";
  if (p.section === "doc") label = `L${p.start}${p.line !== p.start ? `–${p.line}` : ""}`;
  if (p.section === "outdated") label = `was L${t.originalLine ?? "?"}`;

  return (
    <details id={"thread-" + t.id} class="thread" open={!t.isResolved}
      onMouseEnter={() => p.section === "doc" && highlight({ start: p.start, end: p.line })} onMouseLeave={() => highlight(null)}>
      <summary>
        <a href="#" onClick={(e) => { e.preventDefault(); if (p.section === "doc") jump(p.line); }}>{label}</a>
        {p.section === "doc" && p.approximate && <span class="label">approximate</span>}
        {t.isResolved && <span class="label">Resolved</span>}
        <span class="muted"> {t.comments[0]?.author}</span>
      </summary>
      {t.comments.map((c) => (
        <div class="comment" key={c.id}>
          <div class="who">
            {c.avatarUrl && <img src={c.avatarUrl} alt="" width={20} height={20} />}
            <b>{c.author}</b>
            {c.state === "PENDING" && <span class="label pending">Pending</span>}
            <a class="muted" href={c.url} target="_blank" rel="noopener noreferrer">{new Date(c.createdAt).toLocaleString()}</a>
          </div>
          <div class="markdown-body" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(c.bodyHTML) }} />
        </div>
      ))}
      <textarea placeholder="Reply…" value={reply} onInput={(e) => setReply(e.currentTarget.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); send(e.shiftKey ? "now" : "review"); }
        }} />
      <div class="row">
        <button class="primary" disabled={busy} onClick={() => send("review")}>Add to review</button>
        {!pendingThread && <button disabled={busy} onClick={() => send("now")}>Reply now</button>}
        <span class="spacer" />
        {!pendingThread && (
          <button disabled={busy} onClick={() => run(() => api.setResolved(t.id, !t.isResolved))}>
            {t.isResolved ? "Unresolve" : "Resolve"}
          </button>
        )}
      </div>
    </details>
  );
}

render(<App />, document.getElementById("app")!);
