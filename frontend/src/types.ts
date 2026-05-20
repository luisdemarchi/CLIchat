export type ProviderId = 'claude' | 'gemini' | 'codex';
export type SessionStatus = 'idle' | 'busy' | 'waiting' | 'offline';
export type SessionOrigin = 'internal' | 'external';
export type MessageRole = 'user' | 'assistant' | 'system';

export interface Provider {
  id: ProviderId;
  name: string;
  cli: string;
  command: string;
  args: string[];
  tag: string;
  accent: string;
  available: boolean;
  description: string;
}

export interface Message {
  id: string;
  role: MessageRole;
  text: string;
  createdAt: string;
}

export interface PendingAction {
  id: string;
  label: string;
  input: string;
}

export interface Session {
  id: string;
  title: string;
  topic?: string;
  providerId: string;
  providerTag: string;
  providerAccent: string;
  origin: SessionOrigin;
  status: SessionStatus;
  cwd?: string;
  avatarLabel: string;
  lastMessage: string;
  currentTool?: string;
  processId?: number;
  tty?: string;
  claudeSessionId?: string;
  externalAttach: string;
  createdAt: string;
  updatedAt: string;
  messages: Message[];
  messageCount?: number;
  pendingQuestion?: string;
  pendingActions: PendingAction[];
  terminalAttached: boolean;
  transcriptPath?: string;
}

export interface MirrorStatus {
  enabled: boolean;
  mode: string;
  address: string;
  note: string;
}

export interface Bootstrap {
  providers: Provider[];
  sessions: Session[];
  selected?: Session;
  mirror: MirrorStatus;
}
