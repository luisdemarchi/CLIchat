import '@xterm/xterm/css/xterm.css';
import { useEffect, useRef } from 'react';
import type { FitAddon as XtermFitAddon } from '@xterm/addon-fit';
import type { Terminal as XtermTerminal } from '@xterm/xterm';
import { sendTerminalInput } from '../lib/api';

interface Props {
  sessionID: string;
  visible?: boolean;
}

function decodeBase64ToBytes(b64: string): Uint8Array {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

export function TerminalPane({ sessionID, visible = true }: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<XtermTerminal | null>(null);
  const fitRef = useRef<XtermFitAddon | null>(null);

  useEffect(() => {
    if (!visible) return;
    const fit = fitRef.current;
    const term = terminalRef.current;
    if (!fit || !term) return;
    const id = window.requestAnimationFrame(() => {
      try {
        const dim = fit.proposeDimensions();
        if (dim && dim.rows > 0) {
          term.resize(120, dim.rows);
        }
      } catch {
        // container size briefly zero
      }
      term.scrollToBottom();
    });
    return () => window.cancelAnimationFrame(id);
  }, [visible]);

  useEffect(() => {
    if (!containerRef.current) return;
    let disposed = false;
    let unsubOutput: (() => void) | undefined;
    let unsubExit: (() => void) | undefined;
    let resizeObserver: ResizeObserver | undefined;
    let dataDisposable: { dispose: () => void } | undefined;

    const FIXED_COLS = 120;

    void Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit')]).then(([xterm, fitAddon]) => {
      if (disposed || !containerRef.current) return;
      const terminal = new xterm.Terminal({
        convertEol: true,
        cursorBlink: true,
        cols: FIXED_COLS,
        rows: 24,
        fontFamily: 'Menlo, Monaco, "SFMono-Regular", Consolas, "Liberation Mono", monospace',
        fontSize: 13,
        lineHeight: 1.2,
        scrollback: 10000,
        allowProposedApi: true,
        theme: {
          background: '#1e1e1e',
          foreground: '#d4d4d4',
          cursor: '#ffffff',
          selectionBackground: '#3a4250',
          // Adjusted "black" so TUIs that paint dimmed/black foreground (codex,
          // some borders) stay visible against the dark background.
          black: '#5a5a5a',
          red: '#f48771',
          green: '#9cdcfe',
          yellow: '#dcdcaa',
          blue: '#569cd6',
          magenta: '#c586c0',
          cyan: '#4ec9b0',
          white: '#d4d4d4',
          brightBlack: '#7a7a7a',
          brightRed: '#f48771',
          brightGreen: '#b5cea8',
          brightYellow: '#dcdcaa',
          brightBlue: '#9cdcfe',
          brightMagenta: '#c586c0',
          brightCyan: '#4ec9b0',
          brightWhite: '#ffffff',
        },
      });
      const fit = new fitAddon.FitAddon();
      terminal.loadAddon(fit);
      terminal.open(containerRef.current);
      const fitRows = () => {
        try {
          const dim = fit.proposeDimensions();
          if (dim && dim.rows > 0) {
            terminal.resize(FIXED_COLS, dim.rows);
          }
        } catch {
          // container may briefly have zero size during layout
        }
      };
      fitRows();
      terminalRef.current = terminal;
      fitRef.current = fit;

      dataDisposable = terminal.onData((data) => {
        void sendTerminalInput({ sessionId: sessionID, data });
      });

      const onOutput = (...args: unknown[]) => {
        const payload = args[0];
        if (typeof payload !== 'string') return;
        try {
          const bytes = decodeBase64ToBytes(payload);
          terminal.write(bytes);
        } catch {
          // skip malformed chunk
        }
      };
      const onExit = () => {
        terminal.writeln('\r\n\x1b[33m[processo encerrado]\x1b[0m');
      };
      unsubOutput = window.runtime?.EventsOn(`terminal:${sessionID}`, onOutput);
      unsubExit = window.runtime?.EventsOn(`terminal:${sessionID}:exit`, onExit);

      resizeObserver = new ResizeObserver(() => {
        fitRows();
      });
      resizeObserver.observe(containerRef.current);
    });

    return () => {
      disposed = true;
      dataDisposable?.dispose();
      unsubOutput?.();
      unsubExit?.();
      resizeObserver?.disconnect();
      terminalRef.current?.dispose();
      terminalRef.current = null;
      fitRef.current = null;
    };
  }, [sessionID]);

  return <div className="xterm-shell" ref={containerRef} />;
}
