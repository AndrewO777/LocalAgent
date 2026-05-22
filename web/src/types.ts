// TypeScript mirrors of the Go side. Keep in sync with:
//   - internal/agent/events.go    (AgentEvent / EventType)
//   - internal/agent/todos.go     (Todo / TodoStatus)
//   - internal/skills/skills.go   (Skill)
//   - internal/server/sessions.go (SessionSummary)

// EventType mirrors agent.EventType in internal/agent/events.go. The legacy
// `error` event type was removed — fatal errors now arrive as a `done` event
// with reason="error" (see DoneReason below).
export type EventType =
  | 'started'
  | 'iteration'
  | 'model_text'
  | 'tool_call'
  | 'tool_result'
  | 'compaction'
  | 'skill_activated'
  | 'question'
  | 'answer'
  | 'todo_update'
  | 'user_message'
  | 'done';

export type DoneReason = 'finished' | 'max_iter' | 'canceled' | 'error' | string;

export type TodoStatus = 'pending' | 'in_progress' | 'completed';

export interface Todo {
  content: string;
  status: TodoStatus;
}

export interface AgentEvent {
  type: EventType;
  time_ms: number;
  iter?: number;
  text?: string;
  tool?: string;
  arguments?: string;
  result?: string;
  is_error?: boolean;
  reason?: DoneReason;
  summary?: string;
  // compaction
  kind?: 'elide' | 'summarize' | 'trim' | string;
  tokens_before?: number;
  tokens_after?: number;
  // skill
  skill?: string;
  // question/answer
  question_id?: string;
  question?: string;
  options?: string[];
  // todos
  todos?: Todo[];
}

export type SkillSource = 'user' | 'project';

export interface Skill {
  name: string;
  description: string;
  source: SkillSource;
  path: string;
}

export type SessionStatus =
  | 'running'
  | 'finished'
  | 'error'
  | 'canceled'
  | 'max_iter'
  | 'unknown';

export interface SessionSummary {
  id: string;
  goal: string;
  model: string;
  workdir: string;
  host?: string;
  started_at: string;
  ended_at?: string;
  status: SessionStatus;
  event_count: number;
  active_skills?: string[];
  todos?: Todo[];
}

export interface SessionDetail extends SessionSummary {
  events: AgentEvent[];
}

// --- request bodies --------------------------------------------------------

export interface RunRequest {
  model: string;
  compaction_model?: string;
  host?: string;
  workdir: string;
  goal: string;
  context_tokens?: number;
  skills?: string[];
  max_iterations?: number;
}

export interface ContinueRequest {
  goal: string;
  host?: string;
  compaction_model?: string;
  context_tokens?: number;
  skills?: string[];
  max_iterations?: number;
}

export interface RunResponse {
  session_id: string;
}

// --- form state (UI-local) --------------------------------------------------

export interface FormState {
  model: string;
  compaction_model: string;
  host: string;
  workdir: string;
  goal: string;
  context_tokens: number;
}

// --- current-session view (live state held in App component) ---------------

export interface CurrentSession {
  id: string;
  goal: string;
  model: string;
  workdir: string;
  host?: string;
  status: SessionStatus;
  events: AgentEvent[];
  todos?: Todo[];
  isLive: boolean;
}
