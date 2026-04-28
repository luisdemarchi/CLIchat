export type ProviderId = 'claude' | 'gemini' | 'codex';
export type SessionStatus = 'idle' | 'busy' | 'waiting' | 'offline';
export type MessageRole = 'user' | 'assistant' | 'system';

export interface Provider {
  id: ProviderId;
  name: string;
  cli: string;
  command: string;
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

export interface Session {
  id: string;
  title: string;
  providerId: ProviderId;
  providerTag: string;
  providerAccent: string;
  status: SessionStatus;
  cwd?: string;
  avatarLabel: string;
  lastMessage: string;
  currentTool?: string;
  processId?: number;
  externalAttach: string;
  createdAt: string;
  updatedAt: string;
  messages: Message[];
  pendingQuestion?: string;
  terminalAttached: boolean;
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
