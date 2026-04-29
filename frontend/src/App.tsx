import { MessageSquarePlus, Moon, Paperclip, Search, Send, Sun, TerminalSquare } from 'lucide-react';
import React, { FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import {
  createChat,
  getBootstrap,
  onStateUpdate,
  pickFiles,
  respondToPrompt,
  selectSession,
  sendFiles,
  sendMessage,
} from './lib/api';
import type { Bootstrap, Message, ProviderId, Session } from './types';
import { TerminalPane } from './components/TerminalPane';
import { ProviderLogo } from './components/ProviderLogo';
import { describeStatus } from './lib/status';

function formatTime(iso: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function meAvatarLabel(name: string): string {
  const parts = name.trim().split(/\s+/);
  if (parts.length === 0) return '?';
  if (parts.length === 1) return parts[0].charAt(0).toUpperCase();
  return (parts[0].charAt(0) + parts[1].charAt(0)).toUpperCase();
}

interface MessageBubbleProps {
  message: Message;
  isFirstOfRun: boolean;
}

// Type-out animation. When a brand-new assistant message arrives we reveal it
// progressively (~80 chars/frame) so long replies do not appear as a wall of
// text after a long wait. Messages restored from history skip the animation.
function useTypewriter(target: string, animate: boolean): string {
  const [shown, setShown] = useState<string>(animate ? '' : target);
  const indexRef = useRef(0);
  useEffect(() => {
    if (!animate) {
      setShown(target);
      return;
    }
    indexRef.current = 0;
    setShown('');
    let cancelled = false;
    const tick = () => {
      if (cancelled) return;
      const next = Math.min(target.length, indexRef.current + 80);
      setShown(target.slice(0, next));
      indexRef.current = next;
      if (next < target.length) {
        requestAnimationFrame(tick);
      }
    };
    const id = requestAnimationFrame(tick);
    return () => {
      cancelled = true;
      cancelAnimationFrame(id);
    };
  }, [target, animate]);
  return shown;
}

function MessageBubble({ message, isFirstOfRun }: MessageBubbleProps) {
  // Track whether this bubble was newly mounted (not a restored history item).
  const mountedAtRef = useRef<number>(Date.now());
  const messageAge = Date.now() - new Date(message.createdAt).getTime();
  // Animate only if mount is recent AND the message is recent (within 5s).
  const animate = messageAge < 5000 && Date.now() - mountedAtRef.current < 200;
  const visibleText = useTypewriter(message.text, animate && message.role === 'assistant');

  if (message.role === 'system') {
    return (
      <article className="bubble system">
        <p>{message.text}</p>
      </article>
    );
  }
  const isAssistant = message.role === 'assistant';
  return (
    <article className={`bubble ${message.role} ${isFirstOfRun ? 'first' : ''}`}>
      <div className="bubble-content">
        {isAssistant ? (
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{visibleText}</ReactMarkdown>
        ) : (
          <p>{message.text}</p>
        )}
      </div>
      <span className="meta">{formatTime(message.createdAt)}</span>
    </article>
  );
}

const LAST_SEEN_KEY = 'clichat:lastSeen';

function readLastSeen(): Record<string, number> {
  try {
    const raw = localStorage.getItem(LAST_SEEN_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    return typeof parsed === 'object' && parsed ? parsed : {};
  } catch {
    return {};
  }
}

function writeLastSeen(map: Record<string, number>) {
  try {
    localStorage.setItem(LAST_SEEN_KEY, JSON.stringify(map));
  } catch {
    // localStorage may be unavailable
  }
}

export function App() {
  const [state, setState] = useState<Bootstrap | null>(null);
  const [selectedID, setSelectedID] = useState('');
  const [draft, setDraft] = useState('');
  const [search, setSearch] = useState('');
  const [error, setError] = useState('');
  const [terminalOpen, setTerminalOpen] = useState(true);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [lastSeen, setLastSeen] = useState<Record<string, number>>(() => readLastSeen());
  const [theme, setTheme] = useState<'light' | 'dark'>(
    () => (document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light'),
  );
  const pickerWrapRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('clichat.theme', theme);
  }, [theme]);

  useEffect(() => {
    if (!pickerOpen) return;
    function onDocClick(ev: MouseEvent) {
      if (!pickerWrapRef.current) return;
      if (!pickerWrapRef.current.contains(ev.target as Node)) {
        setPickerOpen(false);
      }
    }
    function onEsc(ev: KeyboardEvent) {
      if (ev.key === 'Escape') setPickerOpen(false);
    }
    document.addEventListener('mousedown', onDocClick);
    document.addEventListener('keydown', onEsc);
    return () => {
      document.removeEventListener('mousedown', onDocClick);
      document.removeEventListener('keydown', onEsc);
    };
  }, [pickerOpen]);

  useEffect(() => {
    getBootstrap().then((payload) => {
      setState(payload);
      setSelectedID(payload.selected?.id ?? payload.sessions[0]?.id ?? '');
    });
    return onStateUpdate((payload) => {
      setState(payload);
      setSelectedID((current) => current || payload.selected?.id || payload.sessions[0]?.id || '');
    });
  }, []);

  const filteredSessions = useMemo(() => {
    if (!state) return [];
    const query = search.trim().toLowerCase();
    if (!query) return state.sessions;
    return state.sessions.filter((s) =>
      [s.title, s.lastMessage, s.topic ?? ''].some((field) => field.toLowerCase().includes(query)),
    );
  }, [state, search]);

  const selected = useMemo(() => {
    if (!state) return undefined;
    return state.sessions.find((s) => s.id === selectedID) ?? state.selected ?? state.sessions[0];
  }, [selectedID, state]);

  const selectedStatus = useMemo(
    () => (selected ? describeStatus(selected) : describeStatus({ status: 'offline' } as Session)),
    [selected],
  );
  const SelectedIcon = selectedStatus.Icon;

  const canSend = Boolean(selected && selected.status !== 'busy' && selected.terminalAttached);

  const messagesRef = useRef<HTMLDivElement | null>(null);
  const lastMessageCount = (selected?.messages ?? []).length;
  useEffect(() => {
    const node = messagesRef.current;
    if (!node) return;
    requestAnimationFrame(() => {
      node.scrollTop = node.scrollHeight;
    });
  }, [selectedID, lastMessageCount, selected?.lastMessage, selected?.status]);

  // Mark the open chat as fully read whenever its message list grows.
  useEffect(() => {
    if (!selected) return;
    setLastSeen((prev) => {
      const current = prev[selected.id] ?? 0;
      const total = (selected.messages ?? []).length;
      if (current >= total) return prev;
      const next = { ...prev, [selected.id]: total };
      writeLastSeen(next);
      return next;
    });
  }, [selected, lastMessageCount]);

  function unreadCount(s: Session): number {
    if (s.id === selectedID) return 0;
    const total = (s.messages ?? []).length;
    const seen = lastSeen[s.id] ?? 0;
    const unread = total - seen;
    return unread > 0 ? unread : 0;
  }

  async function handleSelect(session: Session) {
    setSelectedID(session.id);
    try {
      await selectSession(session.id);
    } catch {
      // optimistic
    }
  }

  async function handleNewChat(provider: { id: ProviderId; name: string }) {
    if (!state) return;
    setPickerOpen(false);
    setError('');
    try {
      const created = await createChat({
        providerId: provider.id,
        title: `${provider.name} ${state.sessions.length + 1}`,
        cwd: '',
      });
      setSelectedID(created.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (!selected || draft.trim() === '') return;
    if (!canSend) {
      setError('Aguarde a resposta atual terminar.');
      return;
    }
    const text = draft.trim();
    setDraft('');
    setError('');
    try {
      await sendMessage({ sessionId: selected.id, text });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handlePromptAction(action: { id: string; input: string }) {
    if (!selected) return;
    setError('');
    try {
      await respondToPrompt({ sessionId: selected.id, actionId: action.id, input: action.input });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleAttach() {
    if (!selected) return;
    if (!canSend) {
      setError('Aguarde a resposta atual terminar.');
      return;
    }
    setError('');
    try {
      const paths = await pickFiles();
      if (!paths || paths.length === 0) return;
      await sendFiles({ sessionId: selected.id, paths });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  if (!state) {
    return <main className="shell loading">Carregando…</main>;
  }

  const messages = selected?.messages ?? [];

  return (
    <main className="shell">
      <aside className="sidebar">
        <header className="sidebar-header">
          <div className="me">
            <div className="me-avatar">{meAvatarLabel('Local Chat')}</div>
            <div>
              <strong>CLIchat</strong>
              <small>{state.mirror.note}</small>
            </div>
          </div>
          <div className="actions">
            <div className="actions-wrap" ref={pickerWrapRef}>
              <button
                className={`icon-btn ${pickerOpen ? 'active' : ''}`}
                type="button"
                title="Novo chat"
                onClick={() => setPickerOpen((v) => !v)}
              >
                <MessageSquarePlus size={20} />
              </button>
              {pickerOpen ? (
                <div className="provider-menu" role="menu">
                  {state.providers.map((p) => (
                    <button
                      key={p.id}
                      type="button"
                      role="menuitem"
                      disabled={!p.available}
                      title={p.available ? p.description : `${p.name} CLI nao encontrado`}
                      onClick={() => {
                        setPickerOpen(false);
                        void handleNewChat({ id: p.id, name: p.name });
                      }}
                    >
                      <span className="logo" style={{ background: p.accent }}>
                        <ProviderLogo providerId={p.id} size={14} />
                      </span>
                      <span className="label">{p.name}</span>
                      {!p.available ? <span className="hint">indisponivel</span> : null}
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
            <button
              className="icon-btn"
              type="button"
              title={theme === 'dark' ? 'Tema claro' : 'Tema escuro'}
              onClick={() => setTheme((t) => (t === 'dark' ? 'light' : 'dark'))}
            >
              {theme === 'dark' ? <Sun size={20} /> : <Moon size={20} />}
            </button>
          </div>
        </header>

        <div className="search-row">
          <div className="field">
            <Search size={16} />
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Pesquisar"
            />
          </div>
        </div>

        {error ? <div className="error-banner">{error}</div> : null}

        <nav className="chat-list" aria-label="Conversas">
          {filteredSessions.length === 0 ? (
            <div className="empty-list">
              <p>Nenhum chat ainda.</p>
              <small>
                Clique no ícone de novo chat acima e escolha o provedor para iniciar uma conversa
                local.
              </small>
            </div>
          ) : null}
          {filteredSessions.map((session) => {
            const info = describeStatus(session);
            const RowIcon = info.Icon;
            const unread = unreadCount(session);
            return (
              <button
                className={`chat-row ${session.id === selected?.id ? 'active' : ''} ${unread > 0 ? 'unread' : ''}`}
                key={session.id}
                type="button"
                onClick={() => void handleSelect(session)}
              >
                <span
                  className={`row-avatar status-${info.className ?? ''} ${info.spin ? 'spin' : ''}`}
                  style={
                    {
                      background: session.providerAccent + '22',
                      color: session.providerAccent,
                      ['--provider-accent' as never]: session.providerAccent,
                    } as React.CSSProperties
                  }
                >
                  <RowIcon size={22} />
                  <span className="badge-emoji">
                    <ProviderLogo providerId={session.providerId} size={13} />
                  </span>
                </span>
                <span className="row-body">
                  <span className="row-line-1">
                    <span className="row-name">{session.topic || session.title}</span>
                    <span className="row-time">{formatTime(session.updatedAt)}</span>
                  </span>
                  <span className="row-line-2">
                    <span className="row-snippet">{session.lastMessage || session.title || '—'}</span>
                    {unread > 0 ? (
                      <span className="unread-badge" title={`${unread} mensagem${unread === 1 ? '' : 'ns'} não lida${unread === 1 ? '' : 's'}`}>
                        {unread > 99 ? '99+' : unread}
                      </span>
                    ) : null}
                  </span>
                </span>
              </button>
            );
          })}
        </nav>
      </aside>

      {selected ? (
        <section className="conversation">
          <header className="conversation-header">
            <div
              className={`header-avatar status-${selectedStatus.className ?? ''} ${selectedStatus.spin ? 'spin' : ''}`}
              style={{ background: selected.providerAccent + '22', color: selected.providerAccent }}
            >
              <SelectedIcon size={22} />
            </div>
            <div className="header-info">
              <span className="header-name">{selected.topic || selected.title}</span>
              {selected.topic && selected.title !== selected.topic ? (
                <span className="header-topic">{selected.title}</span>
              ) : null}
              <span className="header-meta">
                {selectedStatus.label}
                {selected.cwd ? ` · ${selected.cwd}` : ''}
              </span>
            </div>
            <div className="header-actions">
              <button
                type="button"
                className={`icon-btn ${terminalOpen ? 'active' : ''}`}
                title={terminalOpen ? 'Esconder terminal' : 'Mostrar terminal'}
                onClick={() => setTerminalOpen((v) => !v)}
              >
                <TerminalSquare size={20} />
              </button>
            </div>
          </header>

          <div className="messages" ref={messagesRef}>
            {messages.length === 0 ? (
              <article className="bubble system">
                <p>Sem mensagens ainda. Digite abaixo para começar.</p>
              </article>
            ) : null}
            {messages.map((message, index) => {
              const prev = index > 0 ? messages[index - 1] : undefined;
              const isFirstOfRun = !prev || prev.role !== message.role;
              return <MessageBubble key={message.id} message={message} isFirstOfRun={isFirstOfRun} />;
            })}
            {(selected.pendingActions ?? []).length > 0 ? (
              <article className="prompt-card">
                <p className="prompt-question">
                  {selected.pendingQuestion || 'O terminal está aguardando uma escolha.'}
                </p>
                <div className="prompt-options">
                  {(selected.pendingActions ?? []).map((action) => {
                    // Split "1. label" into key + label so we can render both nicely.
                    const m = /^(\S+)\.\s+(.+)$/.exec(action.label);
                    const optKey = m ? m[1] : action.id;
                    const optText = m ? m[2] : action.label;
                    return (
                      <button type="button" key={action.id} onClick={() => void handlePromptAction(action)}>
                        <span className="opt-key">{optKey}</span>
                        <span className="opt-label">{optText}</span>
                      </button>
                    );
                  })}
                </div>
              </article>
            ) : null}
            {selected.status !== 'idle' && selected.status !== 'offline' ? (
              <button
                type="button"
                className={`status-pill clickable ${selectedStatus.spin ? 'spin-icon' : ''} status-${selectedStatus.className ?? ''}`}
                title={terminalOpen ? 'Esconder terminal' : 'Mostrar terminal pra ver detalhes'}
                onClick={() => setTerminalOpen(true)}
              >
                <SelectedIcon size={14} />
                <span>{selectedStatus.label}…</span>
              </button>
            ) : null}
          </div>

          {state.sessions.map((s) => {
            const isThisSelected = s.id === selected.id;
            const visible = isThisSelected && terminalOpen;
            return (
              <section key={s.id} className={`terminal-panel ${visible ? '' : 'hidden'}`}>
                <header>
                  <strong>Terminal · {s.providerTag}</strong>
                  <button type="button" onClick={() => setTerminalOpen(false)}>
                    Ocultar
                  </button>
                </header>
                <TerminalPane sessionID={s.id} visible={visible} />
              </section>
            );
          })}

          <form className="composer" onSubmit={handleSubmit}>
            <button
              type="button"
              className="send-btn"
              title="Anexar arquivos"
              onClick={() => void handleAttach()}
              disabled={!canSend}
            >
              <Paperclip size={20} />
            </button>
            <input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder="Mensagem"
              disabled={!canSend}
            />
            <button type="submit" className="send-btn" title="Enviar" disabled={!canSend}>
              <Send size={20} />
            </button>
          </form>
        </section>
      ) : (
        <section className="empty-conversation">
          <div className="pad">
            <TerminalSquare size={120} strokeWidth={1} />
            <strong>CLIchat</strong>
            <span>
              Clique no ícone de novo chat para iniciar uma conversa com Claude, Gemini ou Codex.
              Cada chat tem seu próprio terminal embutido com a TUI real do CLI.
            </span>
          </div>
        </section>
      )}
    </main>
  );
}
