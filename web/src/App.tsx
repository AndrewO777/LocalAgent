import { useCallback, useEffect, useMemo, useState } from 'react';
import { api } from './api/client';
import { MainPanel } from './components/MainPanel';
import { Sidebar } from './components/Sidebar';
import { useLocalStorage } from './hooks/useLocalStorage';
import { useSSE } from './hooks/useSSE';
import type {
  AgentEvent,
  CurrentSession,
  FormState,
  SessionStatus,
  SessionSummary,
  Skill,
  Todo,
} from './types';

const SLASH_RE = /^\/([a-z][a-z0-9-]{0,63})(?:\s+|$)/;

export function App() {
  // --- form state (persisted in localStorage) ----------------------------
  const [model, setModel] = useLocalStorage('oa.model', 'qwen2.5-coder:7b');
  const [compactionModel, setCompactionModel] = useLocalStorage('oa.compactionModel', '');
  const [host, setHost] = useLocalStorage('oa.host', '');
  const [workdir, setWorkdir] = useLocalStorage('oa.workdir', '.');
  const [contextTokens, setContextTokens] = useLocalStorage(
    'oa.contextTokens',
    32768,
    (s) => Number(s) || 32768,
    (n) => String(n),
  );
  const [selectedSkills, setSelectedSkills] = useLocalStorage<string[]>(
    'oa.selectedSkills',
    [],
    (s) => {
      try {
        return JSON.parse(s) as string[];
      } catch {
        return [];
      }
    },
    (v) => JSON.stringify(v),
  );

  // Non-persisted state.
  const [goal, setGoal] = useState('');
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [current, setCurrent] = useState<CurrentSession | null>(null);
  const [skillCatalog, setSkillCatalog] = useState<Skill[]>([]);
  // continueHost is the override-host input shown in the continue banner.
  // Kept separate from form.host (which is localStorage-backed and used in
  // fresh-run mode) so the user can't accidentally clobber their saved
  // default when overriding for a single continue. Empty means "use the
  // session's stored host" — the server applies that fallback.
  const [continueHost, setContinueHost] = useState('');

  // --- form bundle for the Sidebar component -----------------------------
  const form: FormState = {
    model,
    compaction_model: compactionModel,
    host,
    workdir,
    goal,
    context_tokens: contextTokens,
  };
  const setForm = (next: FormState) => {
    if (next.model !== model) setModel(next.model);
    if (next.compaction_model !== compactionModel) setCompactionModel(next.compaction_model);
    if (next.host !== host) setHost(next.host);
    if (next.workdir !== workdir) setWorkdir(next.workdir);
    if (next.goal !== goal) setGoal(next.goal);
    if (next.context_tokens !== contextTokens) setContextTokens(next.context_tokens);
  };

  // --- session list refresh --------------------------------------------------
  const refreshSessions = useCallback(async () => {
    try {
      const list = await api.listSessions();
      setSessions(list);
    } catch {
      // ignore — polling will retry shortly
    }
  }, []);

  useEffect(() => {
    refreshSessions();
    const t = window.setInterval(refreshSessions, 5000);
    return () => window.clearInterval(t);
  }, [refreshSessions]);

  // --- skill catalog refresh (when workdir changes) ----------------------
  useEffect(() => {
    if (!workdir) {
      setSkillCatalog([]);
      return;
    }
    const ac = new AbortController();
    api
      .listSkills(workdir, ac.signal)
      .then((data) => setSkillCatalog(data.skills || []))
      .catch(() => {
        /* directory may not exist yet; that's fine */
      });
    return () => ac.abort();
  }, [workdir]);

  // --- derived: slash-command parsing in the goal ------------------------
  const slashSkillsInGoal = useMemo(() => {
    if (!goal) return [];
    const known = new Set(skillCatalog.map((s) => s.name));
    const out: string[] = [];
    let g = goal.trimStart();
    for (let i = 0; i < 8; i++) {
      const m = g.match(SLASH_RE);
      if (!m) break;
      if (!known.has(m[1])) break;
      if (!out.includes(m[1])) out.push(m[1]);
      g = g.slice(m[0].length).trimStart();
    }
    return out;
  }, [goal, skillCatalog]);

  const effectiveSkills = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const n of selectedSkills) {
      if (!seen.has(n)) {
        seen.add(n);
        out.push(n);
      }
    }
    for (const n of slashSkillsInGoal) {
      if (!seen.has(n)) {
        seen.add(n);
        out.push(n);
      }
    }
    return out;
  }, [selectedSkills, slashSkillsInGoal]);

  const toggleSkill = useCallback(
    (name: string) => {
      setSelectedSkills(
        selectedSkills.includes(name)
          ? selectedSkills.filter((x) => x !== name)
          : [...selectedSkills, name],
      );
    },
    [selectedSkills, setSelectedSkills],
  );

  // --- derived: current todos + pending question -------------------------
  const currentTodos: Todo[] = useMemo(() => {
    if (current?.events) {
      for (let i = current.events.length - 1; i >= 0; i--) {
        const e = current.events[i];
        if (e.type === 'todo_update') return e.todos || [];
      }
    }
    return current?.todos || [];
  }, [current?.events, current?.todos]);

  const pendingQuestion: AgentEvent | null = useMemo(() => {
    if (!current?.events) return null;
    const answered = new Set<string>();
    for (const e of current.events) {
      if (e.type === 'answer' && e.question_id) answered.add(e.question_id);
    }
    for (let i = current.events.length - 1; i >= 0; i--) {
      const e = current.events[i];
      if (e.type === 'question' && e.question_id && !answered.has(e.question_id)) return e;
    }
    return null;
  }, [current?.events]);

  // --- SSE wiring ---------------------------------------------------------
  const isLive = current?.isLive ?? false;
  const sseSessionId = isLive && current ? current.id : null;

  useSSE(
    sseSessionId,
    (ev) => {
      setCurrent((prev) => {
        if (!prev) return prev;
        return { ...prev, events: [...prev.events, ev] };
      });
    },
    (reason) => {
      // Terminal event arrived. Mark non-live; refresh session list.
      setCurrent((prev) =>
        prev ? { ...prev, status: (reason as SessionStatus) || 'unknown', isLive: false } : prev,
      );
      refreshSessions();
    },
  );

  // --- run actions --------------------------------------------------------
  const isContinue = !!current && !current.isLive && current.status !== 'running';

  // Reset the host-override field every time we transition to a (different)
  // continue target. The user starts with no override; an empty value means
  // "use whatever host the session originally ran against."
  useEffect(() => {
    if (isContinue) setContinueHost('');
  }, [isContinue, current?.id]);

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitError(null);
    try {
      if (isContinue && current) {
        const { session_id } = await api.continueRun(current.id, {
          goal,
          host: continueHost, // empty → server uses session's stored host
          compaction_model: compactionModel,
          context_tokens: contextTokens || 0,
          skills: effectiveSkills,
        });
        // Clear events so SSE replay rebuilds the timeline from scratch.
        // Without this, the prior run's events would appear twice — once
        // from the original REST detail load, once from SSE history replay.
        setCurrent((prev) =>
          prev ? { ...prev, status: 'running', isLive: true, id: session_id, events: [] } : prev,
        );
        setGoal('');
      } else {
        const { session_id } = await api.startRun({
          model,
          compaction_model: compactionModel,
          host,
          workdir,
          goal,
          context_tokens: contextTokens || 0,
          skills: effectiveSkills,
        });
        setCurrent({
          id: session_id,
          goal,
          model,
          workdir,
          host,
          status: 'running',
          events: [],
          isLive: true,
        });
      }
      refreshSessions();
    } catch (err) {
      setSubmitError(String((err as Error).message || err));
    }
  };

  const onCancel = useCallback(async () => {
    if (!current?.id) return;
    try {
      await api.cancelRun(current.id);
    } catch {
      /* ignore — UI will reflect cancel via SSE done event */
    }
  }, [current?.id]);

  const onPickSession = useCallback(async (id: string) => {
    setSubmitError(null);
    try {
      const detail = await api.getSession(id);
      const isLive = detail.status === 'running';
      // For finished sessions: show the REST-fetched events (SSE won't
      // be opened). For running sessions: start with an empty list and
      // let SSE replay history — otherwise REST-fetched events plus
      // SSE-replayed events would render twice.
      setCurrent({
        id: detail.id,
        goal: detail.goal,
        model: detail.model,
        workdir: detail.workdir,
        host: detail.host,
        status: detail.status,
        events: isLive ? [] : (detail.events || []),
        todos: detail.todos || [],
        isLive,
      });
    } catch (err) {
      setSubmitError(String((err as Error).message || err));
    }
  }, []);

  const onDeleteSession = useCallback(
    async (id: string, e: React.MouseEvent) => {
      e.stopPropagation();
      if (!window.confirm("Delete this session? This can't be undone.")) return;
      try {
        await api.deleteSession(id);
      } catch {
        /* ignore */
      }
      if (current?.id === id) setCurrent(null);
      refreshSessions();
    },
    [current?.id, refreshSessions],
  );

  const onNewRun = useCallback(() => {
    setCurrent(null);
    setSubmitError(null);
  }, []);

  // --- layout -------------------------------------------------------------
  return (
    <div className="grid grid-cols-[360px_1fr] h-full">
      <Sidebar
        form={form}
        setForm={setForm}
        isContinue={isContinue}
        current={current}
        continueHost={continueHost}
        setContinueHost={setContinueHost}
        submitError={submitError}
        skillCatalog={skillCatalog}
        selectedSkills={selectedSkills}
        slashSkillsInGoal={slashSkillsInGoal}
        effectiveSkillCount={effectiveSkills.length}
        onToggleSkill={toggleSkill}
        sessions={sessions}
        onPickSession={onPickSession}
        onDeleteSession={onDeleteSession}
        onNewRun={current && !current.isLive ? onNewRun : null}
        onSubmit={onSubmit}
        onCancel={onCancel}
      />
      <MainPanel current={current} todos={currentTodos} pendingQuestion={pendingQuestion} />
    </div>
  );
}
