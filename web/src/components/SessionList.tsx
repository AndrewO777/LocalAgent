import type { SessionSummary } from '../types';

interface Props {
  sessions: SessionSummary[];
  currentId: string | null;
  onPick: (id: string) => void;
  onDelete: (id: string, e: React.MouseEvent) => void;
  onNew: (() => void) | null;
}

function fmtTime(iso: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  if (sameDay)
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  const diffDays = Math.floor((+now - +d) / 86_400_000);
  if (diffDays < 7) {
    return (
      d.toLocaleDateString([], { weekday: 'short' }) +
      ' ' +
      d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    );
  }
  return d.toLocaleDateString();
}

const STATUS_DOT_CLASSES: Record<string, string> = {
  running: 'bg-yellow animate-pulse-dot',
  finished: 'bg-green',
  error: 'bg-red',
  canceled: 'bg-red',
  max_iter: 'bg-red',
};

export function SessionList({ sessions, currentId, onPick, onDelete, onNew }: Props) {
  return (
    <div>
      <div className="flex items-center justify-between">
        <h2 className="text-[11px] font-semibold text-muted uppercase tracking-wider m-0">
          Sessions
        </h2>
        {onNew && (
          <button
            type="button"
            className="bg-transparent text-fg border border-border rounded-md px-2.5 py-1.5 text-xs font-medium hover:border-accent hover:text-accent"
            onClick={onNew}
          >
            New
          </button>
        )}
      </div>
      <div className="flex flex-col gap-1 mt-2">
        {sessions.length === 0 ? (
          <div className="text-muted text-xs italic py-1">No sessions yet.</div>
        ) : (
          sessions.map((s) => {
            const isActive = currentId === s.id;
            return (
              <div
                key={s.id}
                onClick={() => onPick(s.id)}
                className={
                  'grid grid-cols-[1fr_auto] gap-1 items-center bg-panel-2 border rounded-md px-2.5 py-2 cursor-pointer transition-colors ' +
                  (isActive
                    ? 'border-accent bg-accent/10'
                    : 'border-border hover:border-accent')
                }
              >
                <div>
                  <div
                    className="text-[13px] text-fg overflow-hidden text-ellipsis whitespace-nowrap max-w-[220px]"
                    title={s.goal}
                  >
                    {s.goal || '(no goal)'}
                  </div>
                  <div className="text-[11px] text-muted flex gap-1.5 items-center mt-0.5">
                    <span
                      className={
                        'w-1.5 h-1.5 rounded-full ' +
                        (STATUS_DOT_CLASSES[s.status] || 'bg-muted')
                      }
                    />
                    <span>{s.status}</span>
                    <span>·</span>
                    <span>{fmtTime(s.started_at)}</span>
                  </div>
                </div>
                <button
                  type="button"
                  title="Delete session"
                  onClick={(e) => onDelete(s.id, e)}
                  className="bg-transparent border-0 text-muted cursor-pointer px-1.5 py-1 text-base leading-none rounded hover:bg-red hover:text-white"
                >
                  ×
                </button>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
