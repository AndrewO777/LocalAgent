import { useEffect, useRef } from 'react';
import type { AgentEvent, CurrentSession, Todo } from '../types';
import { EventRow } from './EventRow';
import { InputBox } from './InputBox';
import { TodosPanel } from './TodosPanel';

interface Props {
  current: CurrentSession | null;
  todos: Todo[];
  pendingQuestion: AgentEvent | null;
}

const STATUS_BORDER: Record<string, string> = {
  running: 'text-yellow border-yellow',
  finished: 'text-green border-green',
  error: 'text-red border-red',
  canceled: 'text-red border-red',
  max_iter: 'text-red border-red',
};

// MainPanel: header (title + status pill + meta) + collapsible Todos panel +
// scrolling events list + persistent InputBox at the bottom (only when live).
export function MainPanel({ current, todos, pendingQuestion }: Props) {
  const eventsEndRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    eventsEndRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' });
  }, [current?.events.length]);

  if (!current) {
    return (
      <main className="flex flex-col h-full overflow-hidden">
        <div className="flex-1 overflow-y-auto px-5 py-4 font-mono text-[13px] leading-relaxed">
          <div className="text-muted text-center mt-10 font-sans">
            Configure on the left, then click <em>Run agent</em>.
            <br />
            Or pick a past session from the list.
          </div>
        </div>
      </main>
    );
  }

  const statusClass = STATUS_BORDER[current.status] || 'text-muted border-border';

  return (
    <main className="flex flex-col h-full overflow-hidden">
      <div className="px-5 py-3.5 border-b border-border flex items-center gap-3 flex-wrap">
        <span className="font-semibold text-fg" title={current.goal}>
          {current.goal.length > 80 ? current.goal.slice(0, 80) + '…' : current.goal}
        </span>
        <span
          className={
            'text-xs px-2 py-0.5 rounded-full bg-panel-2 border ' + statusClass
          }
        >
          {current.status}
        </span>
        <div className="flex-1" />
        <div className="text-xs text-muted flex gap-2.5 items-center">
          <span>{current.model}</span>
          <span>·</span>
          <code title={current.workdir}>
            {current.workdir.length > 40 ? '…' + current.workdir.slice(-40) : current.workdir}
          </code>
          <span>·</span>
          <code title={current.id}>{current.id.slice(0, 8)}</code>
        </div>
      </div>

      <TodosPanel todos={todos} />

      <div className="flex-1 overflow-y-auto px-5 py-4 font-mono text-[13px] leading-relaxed">
        {current.events.length === 0 ? (
          <div className="text-muted text-center mt-10 font-sans">Waiting for first event…</div>
        ) : (
          current.events.map((e, i) => <EventRow key={i} ev={e} />)
        )}
        <div ref={eventsEndRef} />
      </div>

      {current.isLive && <InputBox sessionId={current.id} question={pendingQuestion} />}
    </main>
  );
}
