import {
  Globe,
  HelpCircle,
  Loader2,
  MessageSquareDot,
  Moon,
  Pencil,
  Search,
  SquareTerminal,
  Zap,
} from 'lucide-react';
import type { ComponentType } from 'react';
import type { Session } from '../types';

interface IconProps {
  size?: number;
}

export interface StatusInfo {
  Icon: ComponentType<IconProps>;
  label: string;
  busy: boolean;
  spin?: boolean;
  className?: string;
}

export function describeStatus(session: Session): StatusInfo {
  if ((session.pendingActions ?? []).length > 0) {
    return { Icon: HelpCircle, label: 'aguardando sua escolha', busy: false, className: 'pending' };
  }

  const tool = session.currentTool ?? '';
  if (session.status === 'offline') return { Icon: Moon, label: 'offline', busy: false, className: 'offline' };
  if (session.status === 'waiting')
    return { Icon: MessageSquareDot, label: 'aguardando voce', busy: false, className: 'waiting' };
  if (session.status === 'busy') {
    if (tool === 'Bash') return { Icon: SquareTerminal, label: 'rodando bash', busy: true, className: 'bash' };
    if (['Read', 'Glob', 'Grep', 'ToolSearch'].includes(tool))
      return { Icon: Search, label: `lendo (${tool})`, busy: true, className: 'read' };
    if (['Write', 'Edit', 'NotebookEdit'].includes(tool))
      return { Icon: Pencil, label: `editando (${tool})`, busy: true, className: 'write' };
    if (tool === 'Agent') return { Icon: Zap, label: 'subagente', busy: true, className: 'agent' };
    if (['WebFetch', 'WebSearch'].includes(tool))
      return { Icon: Globe, label: `web (${tool})`, busy: true, className: 'web' };
    if (tool) return { Icon: Loader2, label: tool, busy: true, spin: true, className: 'busy' };
    return { Icon: Loader2, label: 'pensando', busy: true, spin: true, className: 'busy' };
  }
  return { Icon: Moon, label: 'ocioso', busy: false, className: 'idle' };
}
