import type { AgentEvent } from '../types';

function prettyArgs(s: string | undefined): string {
  if (!s) return '(no arguments)';
  try {
    const compact = JSON.stringify(JSON.parse(s));
    return compact.length > 120 ? compact.slice(0, 120) + '…' : compact;
  } catch {
    return s.length > 120 ? s.slice(0, 120) + '…' : s;
  }
}

// EventRow renders one event from the agent's stream. Every EventType has its
// own case so the visual identity stays clear (colour, glyph, formatting).
export function EventRow({ ev }: { ev: AgentEvent }) {
  switch (ev.type) {
    case 'started':
      return <div className="py-1.5 text-muted">▶ goal: {ev.text}</div>;

    case 'iteration':
      return (
        <div className="pt-3.5 pb-1 mt-2.5 border-t border-border first:border-t-0 first:mt-0 text-accent font-bold">
          ── iteration {ev.iter} ──
        </div>
      );

    case 'model_text':
      return <div className="py-1.5 whitespace-pre-wrap text-fg">{ev.text}</div>;

    case 'tool_call':
      return (
        <div className="py-1.5 text-yellow">
          → <span className="text-purple font-semibold">{ev.tool}</span>
          <details className="mt-0.5">
            <summary className="cursor-pointer text-muted text-xs">{prettyArgs(ev.arguments)}</summary>
            <pre className="mt-1 bg-panel-2 border border-border rounded px-2 py-1.5 whitespace-pre-wrap break-all">
              {ev.arguments}
            </pre>
          </details>
        </div>
      );

    case 'tool_result':
      return (
        <div
          className={
            'py-1.5 pl-4 whitespace-pre-wrap border-l-2 ' +
            (ev.is_error ? 'border-red text-red' : 'border-border text-muted')
          }
        >
          {ev.result}
        </div>
      );

    case 'compaction': {
      const before = ev.tokens_before ?? 0;
      const after = ev.tokens_after ?? 0;
      const delta = before - after;
      const arrow =
        ev.tokens_before != null && ev.tokens_after != null
          ? ` (${before} → ${after} tok, ${delta >= 0 ? '−' : '+'}${Math.abs(delta)})`
          : '';
      return (
        <div
          className={
            'py-1.5 pl-4 italic border-l-2 border-dotted ' +
            (ev.is_error ? 'border-red text-red' : 'border-purple text-purple')
          }
        >
          ⌬ {ev.kind}: {ev.text}
          {arrow}
        </div>
      );
    }

    case 'skill_activated':
      return (
        <div className="py-1.5 pl-4 border-l-2 border-accent text-accent">
          ✦ skill activated: <span className="font-bold">{ev.skill}</span>
          {ev.text && <div className="text-xs text-muted mt-0.5">{ev.text}</div>}
        </div>
      );

    case 'question':
      return (
        <div className="mt-2 px-3 py-2.5 border-l-[3px] border-yellow bg-yellow/10 rounded-r-md text-yellow">
          <div className="font-bold uppercase text-[11px] tracking-wider mb-1">? agent asks</div>
          <div className="text-fg whitespace-pre-wrap">{ev.question}</div>
        </div>
      );

    case 'answer':
      return (
        <div className="py-1.5 pl-4 border-l-[3px] border-green text-green whitespace-pre-wrap">
          <span className="font-bold uppercase text-[11px] tracking-wider mr-2">↳ you</span>
          {ev.text || <em className="text-muted">(empty)</em>}
        </div>
      );

    case 'todo_update': {
      const todos = ev.todos || [];
      const done = todos.filter((t) => t.status === 'completed').length;
      return (
        <div
          className="py-1.5 pl-4 italic border-l-2 border-dotted border-purple text-purple"
          title="See the Todos panel above for the current plan"
        >
          ▤ todos updated ({done}/{todos.length} complete)
        </div>
      );
    }

    case 'user_message':
      return (
        <div
          className="py-1.5 pl-4 border-l-[3px] border-green text-green whitespace-pre-wrap"
          title="Mid-run message you sent"
        >
          <span className="font-bold uppercase text-[11px] tracking-wider mr-2">↳ you</span>
          {ev.text}
        </div>
      );

    case 'done':
      return (
        <div
          className={
            'pt-3.5 font-bold ' +
            (ev.reason === 'error'
              ? 'text-red'
              : ev.reason === 'canceled' || ev.reason === 'max_iter'
              ? 'text-yellow'
              : 'text-green')
          }
        >
          {ev.reason === 'finished' && '✓ finished'}
          {ev.reason === 'max_iter' && '⚠ reached max iterations'}
          {ev.reason === 'canceled' && '⚠ canceled'}
          {ev.reason === 'error' && `✗ error: ${ev.text || 'unknown'}`}
          {ev.summary && (
            <span className="ml-1.5 inline-block px-1.5 py-px rounded bg-panel-2 border border-border text-[11px] text-muted font-normal">
              {ev.summary}
            </span>
          )}
        </div>
      );

    default:
      return null;
  }
}
