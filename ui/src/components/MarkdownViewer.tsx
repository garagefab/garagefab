import { useMemo } from 'react';

interface MarkdownViewerProps {
  content: string;
}

/**
 * Lightweight, safe Markdown renderer for specs, intents, and evidence reports.
 * Emphasizes acceptance criteria (AC-<n>) and code blocks without heavy external dependencies.
 */
export function MarkdownViewer({ content }: MarkdownViewerProps) {
  const renderedBlocks = useMemo(() => {
    if (!content) return [];

    const lines = content.split('\n');
    const blocks: { type: 'h1' | 'h2' | 'h3' | 'code' | 'p' | 'li' | 'ac'; text: string }[] = [];
    let inCode = false;
    let codeBuffer: string[] = [];

    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];

      if (line.trim().startsWith('```')) {
        if (inCode) {
          blocks.push({ type: 'code', text: codeBuffer.join('\n') });
          codeBuffer = [];
          inCode = false;
        } else {
          inCode = true;
        }
        continue;
      }

      if (inCode) {
        codeBuffer.push(line);
        continue;
      }

      const trimmed = line.trim();
      if (!trimmed) continue;

      if (trimmed.startsWith('# ')) {
        blocks.push({ type: 'h1', text: trimmed.substring(2) });
      } else if (trimmed.startsWith('## ')) {
        blocks.push({ type: 'h2', text: trimmed.substring(3) });
      } else if (trimmed.startsWith('### ')) {
        blocks.push({ type: 'h3', text: trimmed.substring(4) });
      } else if (trimmed.startsWith('- [ ]') || trimmed.startsWith('- [x]') || trimmed.startsWith('* [ ]') || trimmed.startsWith('* [x]')) {
        blocks.push({ type: 'li', text: trimmed.substring(2) });
      } else if (trimmed.startsWith('- ') || trimmed.startsWith('* ')) {
        blocks.push({ type: 'li', text: trimmed.substring(2) });
      } else if (trimmed.includes('AC-') || trimmed.startsWith('AC-')) {
        blocks.push({ type: 'ac', text: trimmed });
      } else {
        blocks.push({ type: 'p', text: trimmed });
      }
    }

    if (inCode && codeBuffer.length > 0) {
      blocks.push({ type: 'code', text: codeBuffer.join('\n') });
    }

    return blocks;
  }, [content]);

  return (
    <div className="space-y-3 font-sans text-sm leading-relaxed text-slate-300">
      {renderedBlocks.map((block, idx) => {
        switch (block.type) {
          case 'h1':
            return (
              <h1 key={idx} className="text-xl font-bold text-white pt-2 border-b border-slate-800 pb-2">
                {block.text}
              </h1>
            );
          case 'h2':
            return (
              <h2 key={idx} className="text-base font-semibold text-indigo-300 pt-3 border-b border-slate-800/80 pb-1">
                {block.text}
              </h2>
            );
          case 'h3':
            return (
              <h3 key={idx} className="text-sm font-semibold text-slate-200 pt-2">
                {block.text}
              </h3>
            );
          case 'code':
            return (
              <pre
                key={idx}
                className="overflow-x-auto rounded-xl border border-slate-800 bg-slate-950 p-3.5 font-mono text-xs text-indigo-200"
              >
                <code>{block.text}</code>
              </pre>
            );
          case 'ac':
            return (
              <div
                key={idx}
                className="rounded-xl border border-indigo-500/30 bg-indigo-500/10 p-3 font-mono text-xs text-indigo-200 shadow-sm"
              >
                {block.text}
              </div>
            );
          case 'li':
            return (
              <div key={idx} className="flex items-start gap-2 pl-2">
                <span className="text-indigo-400 mt-1">•</span>
                <span>{block.text}</span>
              </div>
            );
          case 'p':
          default:
            return (
              <p key={idx} className="text-slate-300">
                {block.text}
              </p>
            );
        }
      })}
    </div>
  );
}
