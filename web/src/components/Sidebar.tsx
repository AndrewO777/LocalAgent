import type { CurrentSession, FormState, Skill, SessionSummary } from '../types';
import { SkillsBlock } from './SkillsBlock';
import { SessionList } from './SessionList';

interface Props {
  form: FormState;
  setForm: (next: FormState) => void;
  isContinue: boolean;
  current: CurrentSession | null;
  continueHost: string;
  setContinueHost: (v: string) => void;
  submitError: string | null;

  skillCatalog: Skill[];
  selectedSkills: string[];
  slashSkillsInGoal: string[];
  effectiveSkillCount: number;
  onToggleSkill: (name: string) => void;

  sessions: SessionSummary[];
  onPickSession: (id: string) => void;
  onDeleteSession: (id: string, e: React.MouseEvent) => void;
  onNewRun: (() => void) | null;

  onSubmit: (e: React.FormEvent) => void;
  onCancel: () => void;
}

// Sidebar holds: header, form (fresh-run OR continue), skills block, session
// list. It's a single component because all the children share form state.
export function Sidebar({
  form,
  setForm,
  isContinue,
  current,
  continueHost,
  setContinueHost,
  submitError,
  skillCatalog,
  selectedSkills,
  slashSkillsInGoal,
  effectiveSkillCount,
  onToggleSkill,
  sessions,
  onPickSession,
  onDeleteSession,
  onNewRun,
  onSubmit,
  onCancel,
}: Props) {
  const isLive = current?.isLive ?? false;

  return (
    <aside className="bg-panel border-r border-border p-5 overflow-y-auto flex flex-col gap-5">
      <div>
        <h1 className="text-lg m-0 mb-1">LocalAgent</h1>
        <div className="text-muted text-xs">Agentic loop · litellm + langchaingo · Ollama</div>
      </div>

      <form onSubmit={onSubmit}>
        {isContinue && current ? (
          <>
            <div className="bg-accent/10 border border-accent rounded-md px-3 py-2.5 mb-3.5">
              <div className="text-xs font-semibold text-accent uppercase tracking-wider">
                Continuing session
              </div>
              <div className="text-xs text-muted mt-1 flex gap-1.5 items-center flex-wrap">
                <span title={current.workdir}>{current.workdir}</span>
                <span>·</span>
                <span>{current.model}</span>
                {current.host && (
                  <>
                    <span>·</span>
                    <span title={`Ollama host: ${current.host}`}>{current.host}</span>
                  </>
                )}
              </div>
            </div>
            <Field
              label={
                <>
                  Ollama host override{' '}
                  <span className="normal-case font-normal text-muted">
                    (blank = keep session&apos;s)
                  </span>
                </>
              }
            >
              <input
                className="field-input"
                value={continueHost}
                onChange={(e) => setContinueHost(e.target.value)}
                placeholder={current.host || 'http://localhost:11434'}
              />
            </Field>
          </>
        ) : (
          <>
            <Field label="Model">
              <input
                className="field-input"
                value={form.model}
                onChange={(e) => setForm({ ...form, model: e.target.value })}
                placeholder="qwen2.5-coder:7b"
              />
            </Field>
            <Field label="Ollama host">
              <input
                className="field-input"
                value={form.host}
                onChange={(e) => setForm({ ...form, host: e.target.value })}
                placeholder="http://localhost:11434"
              />
            </Field>
            <Field label="Project directory">
              <input
                className="field-input"
                value={form.workdir}
                onChange={(e) => setForm({ ...form, workdir: e.target.value })}
                placeholder="C:\path\to\project"
              />
            </Field>
            <Field
              label={
                <>
                  Compaction model{' '}
                  <span className="normal-case font-normal text-muted">(optional)</span>
                </>
              }
            >
              <input
                className="field-input"
                value={form.compaction_model}
                onChange={(e) => setForm({ ...form, compaction_model: e.target.value })}
                placeholder="leave blank to reuse main model"
              />
            </Field>
          </>
        )}

        <Field label={isContinue ? 'Next instruction' : 'Goal'}>
          <textarea
            className="field-input resize-y min-h-[80px]"
            value={form.goal}
            onChange={(e) => setForm({ ...form, goal: e.target.value })}
            placeholder={
              isContinue
                ? 'Now also add metrics for the /healthz endpoint'
                : 'Add a /healthz endpoint and a test for it'
            }
            required
          />
        </Field>

        <Field
          label={
            <>
              Context window (tokens){' '}
              <span className="normal-case font-normal text-muted">· 0 disables compaction</span>
            </>
          }
        >
          <input
            type="number"
            min={0}
            max={1_000_000}
            step={1024}
            className="field-input"
            value={form.context_tokens}
            onChange={(e) => setForm({ ...form, context_tokens: Number(e.target.value) })}
          />
        </Field>

        <SkillsBlock
          catalog={skillCatalog}
          selected={selectedSkills}
          slashSkills={slashSkillsInGoal}
          onToggle={onToggleSkill}
          effectiveCount={effectiveSkillCount}
        />

        {submitError && <div className="text-red text-xs mb-2.5">{submitError}</div>}

        {isLive ? (
          <button
            type="button"
            onClick={onCancel}
            className="w-full bg-red text-white border-0 rounded-md py-2.5 px-3.5 font-semibold cursor-pointer text-sm"
          >
            Cancel run
          </button>
        ) : (
          <button
            type="submit"
            disabled={!form.goal.trim()}
            className="w-full bg-accent text-bg border-0 rounded-md py-2.5 px-3.5 font-semibold cursor-pointer text-sm disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {isContinue ? 'Continue session' : 'Run agent'}
          </button>
        )}
      </form>

      <SessionList
        sessions={sessions}
        currentId={current?.id ?? null}
        onPick={onPickSession}
        onDelete={onDeleteSession}
        onNew={onNewRun}
      />
    </aside>
  );
}

function Field({ label, children }: { label: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col mb-3.5">
      <label className="field-label">{label}</label>
      {children}
    </div>
  );
}
