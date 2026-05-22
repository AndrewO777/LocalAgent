import { useEffect, useRef } from 'react';
import type { AgentEvent, EventType } from '../types';

const ALL_EVENT_TYPES: EventType[] = [
  'started',
  'iteration',
  'model_text',
  'tool_call',
  'tool_result',
  'compaction',
  'skill_activated',
  'question',
  'answer',
  'todo_update',
  'user_message',
  'done',
];

// useSSE subscribes to /api/sessions/<id>/events and invokes `onEvent` for
// every server-side event. EventSource handles auto-reconnect; the cleanup
// closes the connection on unmount or when sessionId becomes null.
//
// Callbacks are stored in refs so changes to onEvent/onDone (which are
// usually freshly created each render) don't force the EventSource to be
// torn down and re-established. Only sessionId changes trigger a new
// connection.
export function useSSE(
  sessionId: string | null,
  onEvent: (e: AgentEvent) => void,
  onDone?: (reason: string) => void,
): void {
  const onEventRef = useRef(onEvent);
  const onDoneRef = useRef(onDone);

  useEffect(() => {
    onEventRef.current = onEvent;
    onDoneRef.current = onDone;
  });

  useEffect(() => {
    if (!sessionId) return;

    const es = new EventSource(`/api/sessions/${sessionId}/events`);

    const handler = (raw: MessageEvent) => {
      let ev: AgentEvent;
      try {
        ev = JSON.parse(raw.data);
      } catch {
        return;
      }
      onEventRef.current(ev);
      if (ev.type === 'done') {
        es.close();
        onDoneRef.current?.(ev.reason || 'unknown');
      }
    };

    for (const t of ALL_EVENT_TYPES) {
      es.addEventListener(t, handler as EventListener);
    }
    // Browsers auto-reconnect on transient errors; nothing to do here.
    es.onerror = () => {};

    return () => {
      es.close();
    };
  }, [sessionId]);
}
