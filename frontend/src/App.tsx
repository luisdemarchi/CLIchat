import {
  ArrowRightLeft,
  MessageSquarePlus,
  Moon,
  Paperclip,
  Play,
  Search,
  Send,
  Square,
  Sun,
  TerminalSquare,
  Trash2,
  ZoomIn,
  ZoomOut,
} from 'lucide-react';
import React, { FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import {
  createChat,
  deleteSession,
  getBootstrap,
  onStateUpdate,
  openSessionTerminal,
  pickFiles,
  respondToPrompt,
  selectSession,
  sendFiles,
  sendMessage,
  sendTerminalInput,
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
  const [terminalHeight, setTerminalHeight] = useState(() => {
    const stored = Number(localStorage.getItem('clichat.terminalHeight'));
    return Number.isFinite(stored) && stored >= 180 ? stored : 340;
  });
  const [pickerOpen, setPickerOpen] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);
  const [lastSeen, setLastSeen] = useState<Record<string, number>>(() => readLastSeen());
  const [theme, setTheme] = useState<'light' | 'dark'>(
    () => (document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light'),
  );
  const FONT_LEVELS = [0.85, 0.95, 1, 1.15, 1.3] as const;
  const [fontLevel, setFontLevel] = useState<number>(() => {
    const stored = Number(localStorage.getItem('clichat.fontLevel'));
    if (Number.isInteger(stored) && stored >= 0 && stored < FONT_LEVELS.length) return stored;
    return 2;
  });
  const pickerWrapRef = useRef<HTMLDivElement | null>(null);
  const [swipe, setSwipe] = useState<{ id: string; dx: number } | null>(null);
  const swipeStartRef = useRef<{ x: number; width: number } | null>(null);
  const lastSwipeDxRef = useRef(0);
  const SWIPE_THRESHOLD = 130;

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('clichat.theme', theme);
  }, [theme]);

  useEffect(() => {
    document.documentElement.style.setProperty('--wa-bubble-scale', String(FONT_LEVELS[fontLevel]));
    localStorage.setItem('clichat.fontLevel', String(fontLevel));
  }, [fontLevel]);

  useEffect(() => {
    localStorage.setItem('clichat.terminalHeight', String(Math.round(terminalHeight)));
  }, [terminalHeight]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (!(e.metaKey || e.ctrlKey)) return;
      if (e.key === '=' || e.key === '+') {
        e.preventDefault();
        setFontLevel((l) => Math.min(FONT_LEVELS.length - 1, l + 1));
      } else if (e.key === '-' || e.key === '_') {
        e.preventDefault();
        setFontLevel((l) => Math.max(0, l - 1));
      } else if (e.key === '0') {
        e.preventDefault();
        setFontLevel(2);
      }
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

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
    if (state.selected && (!selectedID || state.selected.id === selectedID)) {
      return state.selected;
    }
    return state.sessions.find((s) => s.id === selectedID) ?? state.sessions[0];
  }, [selectedID, state]);

  const selectedStatus = useMemo(
    () => (selected ? describeStatus(selected) : describeStatus({ status: 'offline' } as Session)),
    [selected],
  );
  const SelectedIcon = selectedStatus.Icon;

  // CLIs accept concurrent input; only disable the composer when the terminal
  // is not actually connected.
  const canSend = Boolean(selected && selected.terminalAttached && selected.status !== 'offline');

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
      const total = selected.messageCount ?? (selected.messages ?? []).length;
      if (current >= total) return prev;
      const next = { ...prev, [selected.id]: total };
      writeLastSeen(next);
      return next;
    });
  }, [selected, lastMessageCount]);

  function unreadCount(s: Session): number {
    if (s.id === selectedID) return 0;
    const total = s.messageCount ?? (s.messages ?? []).length;
    const seen = lastSeen[s.id] ?? 0;
    const unread = total - seen;
    return unread > 0 ? unread : 0;
  }

  async function handleSelect(session: Session) {
    setSelectedID(session.id);
    try {
      const full = await selectSession(session.id);
      setState((prev) => {
        if (!prev) return prev;
        return {
          ...prev,
          selected: full,
          sessions: prev.sessions.map((s) => (s.id === full.id ? full : s)),
        };
      });
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

  async function handleOpenSessionTerminal(providerId?: ProviderId) {
    if (!selected) return;
    setError('');
    setTransferOpen(false);
    try {
      const updated = await openSessionTerminal({ sessionId: selected.id, providerId });
      setSelectedID(updated.id);
      setTerminalOpen(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  function handleTerminalResizeStart(event: React.PointerEvent<HTMLDivElement>) {
    event.preventDefault();
    const startY = event.clientY;
    const startHeight = terminalHeight;
    const onMove = (move: PointerEvent) => {
      const maxHeight = Math.max(220, window.innerHeight - 180);
      const next = startHeight + (startY - move.clientY);
      setTerminalHeight(Math.min(maxHeight, Math.max(180, next)));
    };
    const onUp = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      document.body.classList.remove('resizing-terminal');
    };
    document.body.classList.add('resizing-terminal');
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp, { once: true });
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (!selected || draft.trim() === '') return;
    if (!canSend) {
      setError('Terminal closed. Open a new terminal to continue this conversation.');
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

  async function handleInterrupt() {
    if (!selected) return;
    setError('');
    try {
      await sendTerminalInput({ sessionId: selected.id, data: '' });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleDeleteSession(sessionId: string, label: string) {
    const confirmed = window.confirm(`Close chat "${label}"? The CLI process will be stopped.`);
    if (!confirmed) return;
    setError('');
    try {
      await deleteSession(sessionId);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleAttach() {
    if (!selected) return;
    if (!canSend) {
      setError('Terminal closed. Open a new terminal to continue this conversation.');
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
    return <main className="shell loading">Loading...</main>;
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
                title="New chat"
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
                      title={p.available ? p.description : `${p.name} CLI not found`}
                      onClick={() => {
                        setPickerOpen(false);
                        void handleNewChat({ id: p.id, name: p.name });
                      }}
                    >
                      <span className="logo" style={{ background: p.accent }}>
                        <ProviderLogo providerId={p.id} size={14} />
                      </span>
                      <span className="label">{p.name}</span>
                      {!p.available ? <span className="hint">unavailable</span> : null}
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
            <button
              className="icon-btn"
              type="button"
              title="Decrease font size (Cmd -)"
              disabled={fontLevel === 0}
              onClick={() => setFontLevel((l) => Math.max(0, l - 1))}
            >
              <ZoomOut size={20} />
            </button>
            <button
              className="icon-btn"
              type="button"
              title="Increase font size (Cmd +)"
              disabled={fontLevel === FONT_LEVELS.length - 1}
              onClick={() => setFontLevel((l) => Math.min(FONT_LEVELS.length - 1, l + 1))}
            >
              <ZoomIn size={20} />
            </button>
            <button
              className="icon-btn"
              type="button"
              title={theme === 'dark' ? 'Light theme' : 'Dark theme'}
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
              placeholder="Search"
            />
          </div>
        </div>

        {error ? <div className="error-banner">{error}</div> : null}

        <nav className="chat-list" aria-label="Conversations">
          {filteredSessions.length === 0 ? (
            <div className="empty-list">
              <p>No chats yet.</p>
              <small>
                Click the new chat icon above and choose a provider to start a local conversation.
              </small>
            </div>
          ) : null}
          {filteredSessions.map((session) => {
            const info = describeStatus(session);
            const isSwiping = swipe?.id === session.id;
            const dx = isSwiping ? swipe!.dx : 0;
            const RowIcon = info.Icon;
            const unread = unreadCount(session);
            return (
              <div className="chat-row-wrap" key={session.id}>
                <div className="chat-row-bg" aria-hidden="true">
                  <Trash2 size={22} />
                  <span>close</span>
                </div>
              <button
                className={`chat-row ${session.id === selected?.id ? 'active' : ''} ${unread > 0 ? 'unread' : ''} ${isSwiping ? 'swiping' : ''}`}
                type="button"
                style={{ transform: dx ? `translateX(${dx}px)` : undefined }}
                onPointerDown={(e) => {
                  if (e.button !== 0 && e.pointerType === 'mouse') return;
                  const el = e.currentTarget;
                  el.setPointerCapture(e.pointerId);
                  swipeStartRef.current = { x: e.clientX, width: el.getBoundingClientRect().width };
                  setSwipe({ id: session.id, dx: 0 });
                }}
                onPointerMove={(e) => {
                  if (!swipeStartRef.current || swipe?.id !== session.id) return;
                  const delta = Math.min(0, e.clientX - swipeStartRef.current.x);
                  setSwipe({ id: session.id, dx: Math.max(delta, -swipeStartRef.current.width) });
                }}
                onPointerUp={(e) => {
                  const el = e.currentTarget;
                  if (el.hasPointerCapture(e.pointerId)) el.releasePointerCapture(e.pointerId);
                  const dxNow = swipe?.id === session.id ? swipe.dx : 0;
                  lastSwipeDxRef.current = dxNow;
                  swipeStartRef.current = null;
                  setSwipe(null);
                  if (dxNow <= -SWIPE_THRESHOLD) {
                    void handleDeleteSession(session.id, session.topic || session.title);
                  }
                }}
                onPointerCancel={(e) => {
                  const el = e.currentTarget;
                  if (el.hasPointerCapture(e.pointerId)) el.releasePointerCapture(e.pointerId);
                  swipeStartRef.current = null;
                  setSwipe(null);
                }}
                onClick={(e) => {
                  if (lastSwipeDxRef.current < -5) {
                    e.preventDefault();
                    lastSwipeDxRef.current = 0;
                    return;
                  }
                  void handleSelect(session);
                }}
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
                    <span className="row-snippet">
                      {(() => {
                        const name = session.topic || session.title;
                        const snippet = session.lastMessage || session.title || '—';
                        return snippet === name ? info.label : snippet;
                      })()}
                    </span>
                    {unread > 0 ? (
                      <span className="unread-badge" title={`${unread} unread message${unread === 1 ? '' : 's'}`}>
                        {unread > 99 ? '99+' : unread}
                      </span>
                    ) : null}
                  </span>
                </span>
              </button>
              </div>
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
              {canSend ? null : (
                <button
                  type="button"
                  className="icon-btn"
                  title="Open a new terminal with memory"
                  onClick={() => void handleOpenSessionTerminal(selected.providerId as ProviderId)}
                >
                  <Play size={20} />
                </button>
              )}
              <div className="actions-wrap">
                <button
                  type="button"
                  className={`icon-btn ${transferOpen ? 'active' : ''}`}
                  title="Transfer conversation to another terminal"
                  onClick={() => setTransferOpen((v) => !v)}
                >
                  <ArrowRightLeft size={20} />
                </button>
                {transferOpen ? (
                  <div className="provider-menu transfer-menu" role="menu">
                    {state.providers.map((p) => (
                      <button
                        key={p.id}
                        type="button"
                        role="menuitem"
                        disabled={!p.available}
                        title={
                          p.available
                            ? `Open ${p.name} in this conversation`
                            : `${p.name} CLI not found`
                        }
                        onClick={() => void handleOpenSessionTerminal(p.id)}
                      >
                        <span className="logo" style={{ background: p.accent }}>
                          <ProviderLogo providerId={p.id} size={14} />
                        </span>
                        <span className="label">{p.name}</span>
                        {p.id === selected.providerId ? <span className="hint">current</span> : null}
                        {!p.available ? <span className="hint">unavailable</span> : null}
                      </button>
                    ))}
                  </div>
                ) : null}
              </div>
              <button
                type="button"
                className={`icon-btn ${terminalOpen ? 'active' : ''}`}
                title={terminalOpen ? 'Hide terminal' : 'Show terminal'}
                onClick={() => setTerminalOpen((v) => !v)}
              >
                <TerminalSquare size={20} />
              </button>
            </div>
          </header>

          <div className="messages" ref={messagesRef}>
            {messages.length === 0 ? (
              <article className="bubble system">
                <p>No messages yet. Type below to start.</p>
              </article>
            ) : null}
            {messages.map((message, index) => {
              const prev = index > 0 ? messages[index - 1] : undefined;
              const isFirstOfRun = !prev || prev.role !== message.role;
              return <MessageBubble key={message.id} message={message} isFirstOfRun={isFirstOfRun} />;
            })}
            {!canSend ? (
              <article className="terminal-recovery">
                <div>
                  <strong>Terminal disconnected</strong>
                  <p>Open a new terminal for this conversation. The chat memory will be sent as startup context.</p>
                </div>
                <div className="terminal-recovery-actions">
                  <button type="button" onClick={() => void handleOpenSessionTerminal(selected.providerId as ProviderId)}>
                    <Play size={15} />
                    <span>Open {selected.providerTag}</span>
                  </button>
                  {state.providers
                    .filter((p) => p.id !== selected.providerId)
                    .map((p) => (
                      <button
                        type="button"
                        key={p.id}
                        disabled={!p.available}
                        onClick={() => void handleOpenSessionTerminal(p.id)}
                      >
                        <ProviderLogo providerId={p.id} size={15} />
                        <span>Use {p.name}</span>
                      </button>
                    ))}
                </div>
              </article>
            ) : null}
            {canSend && (selected.pendingActions ?? []).length > 0 ? (
              <article className="prompt-card">
                <p className="prompt-question">
                  {selected.pendingQuestion || 'The terminal is waiting for a choice.'}
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
              <div className="status-pill-wrap">
                <button
                  type="button"
                  className={`status-pill clickable ${selectedStatus.spin ? 'spin-icon' : ''} status-${selectedStatus.className ?? ''}`}
                  title={terminalOpen ? 'Hide terminal' : 'Show terminal details'}
                  onClick={() => setTerminalOpen(true)}
                >
                  <SelectedIcon size={14} />
                  <span>{selectedStatus.label}…</span>
                </button>
                {selectedStatus.busy ? (
                  <button
                    type="button"
                    className="status-stop"
                    title="Interrupt (ESC)"
                    aria-label="Interrupt"
                    onClick={() => void handleInterrupt()}
                  >
                    <Square size={12} fill="currentColor" />
                  </button>
                ) : null}
              </div>
            ) : null}
          </div>

          {selected && selected.terminalAttached && terminalOpen ? (
            <section className="terminal-panel" style={{ height: terminalHeight }}>
              <div
                className="terminal-resizer"
                role="separator"
                aria-orientation="horizontal"
                title="Resize terminal"
                onPointerDown={handleTerminalResizeStart}
              />
              <header>
                <strong>Terminal · {selected.providerTag}</strong>
                <button type="button" onClick={() => setTerminalOpen(false)}>
                  Hide
                </button>
              </header>
              <TerminalPane sessionID={selected.id} visible />
            </section>
          ) : null}

          <form className={`composer ${canSend ? '' : 'disabled'}`} onSubmit={handleSubmit}>
            <button
              type="button"
              className="send-btn"
              title="Attach files"
              onClick={() => void handleAttach()}
              disabled={!canSend}
            >
              <Paperclip size={20} />
            </button>
            <input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              placeholder={canSend ? 'Message' : 'Terminal closed. Open a new terminal above'}
              disabled={!canSend}
            />
            <button type="submit" className="send-btn" title="Send" disabled={!canSend}>
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
              Click the new chat icon to start a conversation with Claude, Gemini, or Codex.
              Each chat has its own embedded terminal with the real CLI TUI.
            </span>
          </div>
        </section>
      )}
    </main>
  );
}
