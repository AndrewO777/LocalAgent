import { useState } from 'react';
import type { Todo } from '../types';

// TodosPanel sits between the header and the events list. It's collapsible
// and shows the latest plan emitted by `update_todos`. ○ pending, ▸ in
// progress, ✓ completed.
export function TodosPanel({ todos }: { todos: Todo[] }) {
  const [collapsed, setCollapsed] = useState(false);

  const total = todos.length;
  const done = todos.filter((t) => t.status === 'completed').length;

  return (
    <div className="bg-panel border-b border-border px-5 py-2.5">
      <div
        className="flex justify-between items-center cursor-pointer select-none text-[11px] font-semibold uppercase tracking-wider text-muted"
        onClick={() => setCollapsed((c) => !c)}
        title="Click to collapse/expand"
      >
        <span>
          {collapsed ? '▸' : '▾'} Todos
        </span>
        <span className="normal-case tracking-normal font-normal text-xs">
          {total === 0 ? 'no plan yet' : `${done}/${total} complete`}
        </span>
      </div>

      {!collapsed &&
        (total === 0 ? (
          <div className="text-muted italic text-xs py-1.5">
            Agent hasn't planned yet. The first action of the run should be a call to{' '}
            <code className="text-fg">update_todos</code>.
          </div>
        ) : (
          <div className="mt-2 flex flex-col gap-1">
            {todos.map((t, i) => (
              <div
                key={i}
                className={
                  'flex items-start gap-2 text-[13px] leading-snug ' +
                  (t.status === 'completed'
                    ? 'text-muted line-through'
                    : t.status === 'in_progress'
                    ? 'text-fg font-semibold'
                    : 'text-fg')
                }
              >
                <span
                  className={
                    'flex-shrink-0 w-4 font-mono font-bold ' +
                    (t.status === 'completed'
                      ? 'text-green'
                      : t.status === 'in_progress'
                      ? 'text-yellow'
                      : 'text-muted')
                  }
                >
                  {t.status === 'completed' ? '✓' : t.status === 'in_progress' ? '▸' : '○'}
                </span>
                <span>{t.content}</span>
              </div>
            ))}
          </div>
        ))}
    </div>
  );
}
