export interface Thread {
  id: string;
  company_id: string;
  channel: "whatsapp" | "dashboard";
  phone_number?: string;
  user_id?: string;
  title: string;
  summary?: string;
  last_message_at: string;
  is_archived: boolean;
  created_at: string;
}

export interface Message {
  id: string;
  thread_id: string;
  role: "user" | "assistant" | "tool" | "system";
  content: string;
  tool_calls?: Record<string, unknown>;
  tokens_in?: number;
  tokens_out?: number;
  latency_ms?: number;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface ToolCallEventData {
  name: string;
  arguments?: Record<string, unknown>;
  result?: Record<string, unknown>;
}

export interface ChatEvent {
  job_id: string;
  thread_id: string;
  type:
    | "started"
    | "delta"
    | "thinking"
    | "tool_call"
    | "tool_result"
    | "iteration"
    | "final"
    | "error";
  content?: string;
  thinking_step?: string;
  tool_call?: ToolCallEventData;
  error?: string;
  metadata?: Record<string, unknown>;
  timestamp: string;
}
