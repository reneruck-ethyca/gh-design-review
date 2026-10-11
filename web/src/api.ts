// Mirrors the Go JSON payloads in internal/server and internal/github.
export type DocStatus = "added" | "modified" | "renamed";
export type Hunk = { start: number; end: number };

export type PR = {
  owner: string; repo: string; number: number; title: string; url: string;
  headSha: string; baseSha: string;
  headOwner: string; headRepo: string;
  viewer: string;
  docs: { path: string; status: DocStatus }[];
};

export type DocFile = {
  path: string; headSha: string; content: string; status: DocStatus;
  hunks: Hunk[]; addedLines: number[]; patchMissing: boolean;
};

export type Comment = {
  id: string; databaseId: string; author: string; avatarUrl: string;
  bodyHTML: string; createdAt: string; url: string; state: "PENDING" | "SUBMITTED";
};

export type Thread = {
  id: string; path: string; subjectType: "LINE" | "FILE";
  startLine: number | null; line: number | null;
  originalStartLine: number | null; originalLine: number | null;
  isResolved: boolean; isOutdated: boolean;
  anchor: { startLine: number; line: number; sha: string } | null;
  comments: Comment[];
};

export type Mode = "review" | "now";
export type CreateThread = {
  path: string; body: string; subjectType: "LINE" | "FILE";
  startLine?: number; line?: number; mode: Mode;
};
export type ReviewEvent = "COMMENT" | "APPROVE" | "REQUEST_CHANGES";
export type Review = { id: string; count: number } | null;

export class ApiError extends Error {
  constructor(public code: string, message: string, public status: number) {
    super(message);
  }
}

const token = document.querySelector<HTMLMetaElement>('meta[name="dr-token"]')?.content ?? "";
let head = "";
// The head SHA the page rendered; the server refuses writes if the PR has moved on.
export const setRenderedHead = (sha: string) => (head = sha);

async function call<T>(method: string, url: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(url, {
      method,
      headers: { "Content-Type": "application/json", "X-DR-Token": token, "X-DR-Head": head },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError("server_down", "The local server is not running. Restart gh design-review.", 0);
  }
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new ApiError(data?.error?.code ?? "error", data?.error?.message ?? res.statusText, res.status);
  return data as T;
}

const q = (path: string) => "?path=" + encodeURIComponent(path);

export const api = {
  pr: () => call<PR>("GET", "/api/pr"),
  file: (path: string) => call<DocFile>("GET", "/api/file" + q(path)),
  threads: (path: string) => call<Thread[]>("GET", "/api/threads" + q(path)),
  createThread: (t: CreateThread) => call("POST", "/api/threads", t),
  reply: (t: Thread, body: string, mode: Mode) =>
    call("POST", `/api/threads/${encodeURIComponent(t.id)}/replies`, { body, mode, commentId: t.comments[0]?.databaseId }),
  setResolved: (id: string, resolved: boolean) =>
    call("POST", `/api/threads/${encodeURIComponent(id)}/${resolved ? "resolve" : "unresolve"}`),
  review: () => call<Review>("GET", "/api/review"),
  submit: (event: ReviewEvent, body: string) => call("POST", "/api/review/submit", { event, body }),
  discard: () => call("DELETE", "/api/review"),
};
