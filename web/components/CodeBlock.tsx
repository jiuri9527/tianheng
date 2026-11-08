"use client";

import { useState } from "react";

export default function CodeBlock({
  code,
  language = "bash",
  filename,
}: {
  code: string;
  language?: string;
  filename?: string;
}) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  };

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card">
      <div className="flex items-center justify-between gap-3 border-b border-border bg-muted/50 px-4 py-2">
        <span className="font-mono text-2xs text-muted-foreground">
          {filename ?? language}
        </span>
        <button
          type="button"
          onClick={copy}
          className="focus-ring inline-flex items-center gap-1.5 rounded-sm px-2 py-1 text-2xs font-medium text-muted-foreground transition-colors duration-quick hover:bg-card-hover hover:text-foreground"
        >
          {copied ? (
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true" className="size-3.5 text-positive-text">
              <path d="M3 8.5l3.2 3.2L13 5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          ) : (
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true" className="size-3.5">
              <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" stroke="currentColor" strokeWidth="1.3" />
              <path d="M10.5 3.5H4A1.5 1.5 0 0 0 2.5 5v6.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
            </svg>
          )}
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre className="no-scrollbar overflow-x-auto px-4 py-4 text-xs leading-relaxed">
        <code className="font-mono text-foreground/90">{code}</code>
      </pre>
    </div>
  );
}
