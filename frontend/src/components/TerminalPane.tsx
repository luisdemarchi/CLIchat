import '@xterm/xterm/css/xterm.css';
import { useEffect, useRef } from 'react';
import type { FitAddon as XtermFitAddon } from '@xterm/addon-fit';
import type { ITheme, Terminal as XtermTerminal } from '@xterm/xterm';
import { resizeTerminal, sendTerminalInput } from '../lib/api';

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

const DARK_THEME: ITheme = {
  background: '#0e1116',
  foreground: '#d6deeb',
  cursor: '#ffffff',
  cursorAccent: '#0e1116',
  selectionBackground: '#264f78',
  selectionForeground: '#ffffff',
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
};

const LIGHT_THEME: ITheme = {
  background: '#fafafa',
  foreground: '#24292f',
  cursor: '#24292f',
  cursorAccent: '#fafafa',
  selectionBackground: '#cce0ff',
  selectionForeground: '#24292f',
  black: '#24292f',
  red: '#cf222e',
  green: '#1a7f37',
  yellow: '#9a6700',
  blue: '#0969da',
  magenta: '#8250df',
  cyan: '#1b7c83',
  white: '#6e7781',
  brightBlack: '#57606a',
  brightRed: '#a40e26',
  brightGreen: '#116329',
  brightYellow: '#7d4e00',
  brightBlue: '#0550ae',
  brightMagenta: '#6639ba',
  brightCyan: '#1b7c83',
  brightWhite: '#24292f',
};

function currentTheme(): ITheme {
  return document.documentElement.dataset.theme === 'light' ? LIGHT_THEME : DARK_THEME;
}

export function TerminalPane({ sessionID, visible = true }: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<XtermTerminal | null>(null);
  const fitRef = useRef<XtermFitAddon | null>(null);
  const stickRef = useRef(true);

  useEffect(() => {
    if (!visible) return;
    const fit = fitRef.current;
    const term = terminalRef.current;
    if (!fit || !term) return;
    const id = window.requestAnimationFrame(() => {
      try {
        fit.fit();
      } catch {
        // container size briefly zero
      }
      if (stickRef.current) term.scrollToBottom();
    });
    return () => window.cancelAnimationFrame(id);
  }, [visible]);

  useEffect(() => {
    if (!containerRef.current) return;
    let disposed = false;
    let unsubOutput: (() => void) | undefined;
    let unsubExit: (() => void) | undefined;
    let resizeObserver: ResizeObserver | undefined;
    let themeObserver: MutationObserver | undefined;
    let dataDisposable: { dispose: () => void } | undefined;
    let scrollDisposable: { dispose: () => void } | undefined;
    let resizeFrame = 0;
    let writeFrame = 0;

    void Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit')]).then(([xterm, fitAddon]) => {
      if (disposed || !containerRef.current) return;
      const terminal = new xterm.Terminal({
        convertEol: true,
        cursorBlink: true,
        cursorStyle: 'block',
        rows: 24,
        cols: 120,
        fontFamily: 'Menlo, Monaco, "SFMono-Regular", Consolas, "Liberation Mono", monospace',
        fontSize: 13,
        lineHeight: 1.2,
        scrollback: 5000,
        smoothScrollDuration: 0,
        allowProposedApi: true,
        allowTransparency: false,
        macOptionIsMeta: true,
        macOptionClickForcesSelection: true,
        rightClickSelectsWord: true,
        theme: currentTheme(),
      });
      const fit = new fitAddon.FitAddon();
      terminal.loadAddon(fit);
      terminal.open(containerRef.current);
      let lastCols = 0;
      let lastRows = 0;
      const fitNow = () => {
        try {
          fit.fit();
          const c = terminal.cols;
          const r = terminal.rows;
          if (c > 0 && r > 0 && (c !== lastCols || r !== lastRows)) {
            lastCols = c;
            lastRows = r;
            void resizeTerminal({ sessionId: sessionID, cols: c, rows: r });
          }
        } catch {
          // container may briefly have zero size during layout
        }
      };
      fitNow();
      terminalRef.current = terminal;
      fitRef.current = fit;
      const writeQueue: Uint8Array[] = [];

      const scheduleFit = () => {
        if (resizeFrame) return;
        resizeFrame = window.requestAnimationFrame(() => {
          resizeFrame = 0;
          fitNow();
        });
      };

      const flushOutput = () => {
        writeFrame = 0;
        if (writeQueue.length === 0) return;
        const total = writeQueue.reduce((sum, chunk) => sum + chunk.length, 0);
        const merged = new Uint8Array(total);
        let offset = 0;
        for (const chunk of writeQueue.splice(0)) {
          merged.set(chunk, offset);
          offset += chunk.length;
        }
        terminal.write(merged, () => {
          if (stickRef.current) terminal.scrollToBottom();
        });
      };

      dataDisposable = terminal.onData((data) => {
        void sendTerminalInput({ sessionId: sessionID, data });
      });

      // sticky bottom: only auto-scroll on new output if user is already at the
      // bottom. once they scroll up, respect their position until they scroll
      // back down.
      scrollDisposable = terminal.onScroll(() => {
        const buf = terminal.buffer.active;
        const atBottom = buf.viewportY + terminal.rows >= buf.length;
        stickRef.current = atBottom;
      });

      const onOutput = (...args: unknown[]) => {
        const payload = args[0];
        if (typeof payload !== 'string') return;
        try {
          const bytes = decodeBase64ToBytes(payload);
          writeQueue.push(bytes);
          if (!writeFrame) writeFrame = window.requestAnimationFrame(flushOutput);
        } catch {
          // skip malformed chunk
        }
      };
      const onExit = () => {
        terminal.writeln('\r\n\x1b[33m[process exited]\x1b[0m');
      };
      unsubOutput = window.runtime?.EventsOn(`terminal:${sessionID}`, onOutput);
      unsubExit = window.runtime?.EventsOn(`terminal:${sessionID}:exit`, onExit);

      resizeObserver = new ResizeObserver(scheduleFit);
      resizeObserver.observe(containerRef.current);

      themeObserver = new MutationObserver(() => {
        terminal.options.theme = currentTheme();
      });
      themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    });

    return () => {
      disposed = true;
      if (resizeFrame) window.cancelAnimationFrame(resizeFrame);
      if (writeFrame) window.cancelAnimationFrame(writeFrame);
      dataDisposable?.dispose();
      scrollDisposable?.dispose();
      unsubOutput?.();
      unsubExit?.();
      resizeObserver?.disconnect();
      themeObserver?.disconnect();
      terminalRef.current?.dispose();
      terminalRef.current = null;
      fitRef.current = null;
    };
  }, [sessionID]);

  return <div className="xterm-shell" ref={containerRef} />;
}
