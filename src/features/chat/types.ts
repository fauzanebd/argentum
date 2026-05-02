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

export interface ChatEvent {
  job_id: string;
  thread_id: string;
  type: "started" | "delta" | "final" | "error";
  content?: string;
  error?: string;
  metadata?: Record<string, unknown>;
  timestamp: string;
}
