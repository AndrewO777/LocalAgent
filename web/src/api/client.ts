import type {
  ContinueRequest,
  RunRequest,
  RunResponse,
  SessionDetail,
  SessionSummary,
  Skill,
} from '../types';

// Centralised fetch wrappers. All endpoints share the same JSON-in / JSON-out
// pattern, so the helpers stay tiny. We throw on non-OK so the caller can
// handle errors with a single try/catch.

async function jsonOrThrow<T>(r: Response): Promise<T> {
  if (!r.ok) {
    const body = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(body.error || `HTTP ${r.status}`);
  }
  return (await r.json()) as T;
}

function emptyOrThrow(r: Response): void {
  if (!r.ok) {
    throw new Error(`HTTP ${r.status}`);
  }
}

export const api = {
  listSessions(): Promise<SessionSummary[]> {
    return fetch('/api/sessions').then((r) => jsonOrThrow<SessionSummary[]>(r));
  },

  getSession(id: string): Promise<SessionDetail> {
    return fetch(`/api/sessions/${id}`).then((r) => jsonOrThrow<SessionDetail>(r));
  },

  deleteSession(id: string): Promise<void> {
    return fetch(`/api/sessions/${id}`, { method: 'DELETE' }).then(emptyOrThrow);
  },

  cancelRun(id: string): Promise<void> {
    return fetch(`/api/sessions/${id}/cancel`, { method: 'POST' }).then(emptyOrThrow);
  },

  startRun(body: RunRequest): Promise<RunResponse> {
    return fetch('/api/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }).then((r) => jsonOrThrow<RunResponse>(r));
  },

  continueRun(id: string, body: ContinueRequest): Promise<RunResponse> {
    return fetch(`/api/sessions/${id}/continue`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }).then((r) => jsonOrThrow<RunResponse>(r));
  },

  answerQuestion(id: string, questionId: string, answer: string): Promise<void> {
    return fetch(`/api/sessions/${id}/answer`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question_id: questionId, answer }),
    }).then(emptyOrThrow);
  },

  injectMessage(id: string, message: string): Promise<void> {
    return fetch(`/api/sessions/${id}/inject`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message }),
    }).then(emptyOrThrow);
  },

  listSkills(workdir: string, signal?: AbortSignal): Promise<{ skills: Skill[]; warnings: string[] }> {
    const url = `/api/skills?workdir=${encodeURIComponent(workdir)}`;
    return fetch(url, { signal }).then((r) =>
      jsonOrThrow<{ skills: Skill[]; warnings: string[] }>(r),
    );
  },
};
