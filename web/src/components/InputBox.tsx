import { useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import type { AgentEvent } from '../types';

interface Props {
  sessionId: string;
  // When set, the input is in "answer" mode and routes to /answer with the
  // question's ID. When null, it's in "inject" mode and routes to /inject.
  question: AgentEvent | null;
}

// InputBox is the persistent chat input at the bottom of the events panel.
// Visible throughout the entire lifetime of a live run; auto-switches between
// answering a pending ask_user question (yellow) and free-form injection
// (blue).
export function InputBox({ sessionId, question }: Props) {
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const taRef = useRef<HTMLTextAreaElement | null>(null);
  const mode: 'answer' | 'inject' = question ? 'answer' : 'inject';

  useEffect(() => {
    if (question) {
      taRef.current?.focus();
      setText('');
      setErr(null);
    }
  }, [question?.question_id]);

  const send = async (payload: string) => {
    // Empty inject is rejected; empty answer is permitted (still meaningful).
    if (!payload.trim() && mode !== 'answer') return;
    setSending(true);
    setErr(null);
    try {
      if (mode === 'answer' && question?.question_id) {
        await api.answerQuestion(sessionId, question.question_id, payload);
      } else {
        await api.injectMessage(sessionId, payload);
      }
      setText('');
    } catch (e) {
      setErr(String((e as Error).message || e));
    } finally {
      setSending(false);
    }
  };

  const onSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!sending) send(text);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !sending) {
      e.preventDefault();
      send(text);
    }
  };

  const isAnswer = mode === 'answer';
  const accentBorder = isAnswer ? 'border-t-yellow' : 'border-t-accent';
  const accentText = isAnswer ? 'text-yellow' : 'text-accent';
  const accentBg = isAnswer ? 'bg-yellow' : 'bg-accent';
  const accentFocus = isAnswer ? 'focus:border-yellow' : 'focus:border-accent';

  return (
    <form
      className={'sticky bottom-0 bg-panel border-t-2 px-5 py-3 ' + accentBorder}
      onSubmit={onSubmit}
    >
      <div className={'text-xs font-semibold mb-1.5 ' + accentText}>
        {isAnswer
          ? 'Agent is waiting for your answer.'
          : "Type a message to the agent. It'll be appended at the start of the next iteration."}
      </div>

      {isAnswer && question?.options && question.options.length > 0 && (
        <div className="flex gap-1.5 flex-wrap mb-2">
          {question.options.map((opt) => (
            <button
              key={opt}
              type="button"
              disabled={sending}
              onClick={() => send(opt)}
              title={`Send "${opt}" as the answer`}
              className="bg-panel-2 text-fg border border-border rounded px-2.5 py-1 cursor-pointer text-xs hover:border-yellow hover:text-yellow disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {opt}
            </button>
          ))}
        </div>
      )}

      <div className="flex gap-2 items-stretch">
        <textarea
          ref={taRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder={
            isAnswer
              ? 'Your answer… (Ctrl/⌘+Enter to send)'
              : 'Send a message to the agent… (Ctrl/⌘+Enter to send)'
          }
          disabled={sending}
          className={
            'flex-1 bg-panel-2 text-fg border border-border rounded-md px-2.5 py-2 font-mono text-[13px] resize-y min-h-[50px] focus:outline-none ' +
            accentFocus
          }
        />
        <button
          type="submit"
          disabled={sending || (!text.trim() && !isAnswer)}
          className={
            'border-0 rounded-md px-4 py-2 font-semibold cursor-pointer text-[#0d1117] disabled:opacity-50 disabled:cursor-not-allowed ' +
            accentBg
          }
        >
          {sending ? '…' : 'Send'}
        </button>
      </div>

      {err && <div className="text-red text-xs mt-1">{err}</div>}
    </form>
  );
}
