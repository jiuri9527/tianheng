"use client";

import { useEffect, useState } from "react";

type Theme = "light" | "dark" | "system";
const STORAGE_KEY = "theme";

const OPTIONS: { value: Theme; label: string; icon: React.ReactNode }[] = [
  {
    value: "light",
    label: "浅色",
    icon: (
      <svg viewBox="0 0 20 20" fill="none" aria-hidden="true" className="size-4">
        <circle cx="10" cy="10" r="3.25" stroke="currentColor" strokeWidth="1.5" />
        <path
          d="M10 1.75v2M10 16.25v2M1.75 10h2M16.25 10h2M4.16 4.16l1.42 1.42M14.42 14.42l1.42 1.42M15.84 4.16l-1.42 1.42M5.58 14.42l-1.42 1.42"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
        />
      </svg>
    ),
  },
  {
    value: "dark",
    label: "深色",
    icon: (
      <svg viewBox="0 0 20 20" fill="none" aria-hidden="true" className="size-4">
        <path
          d="M16.5 12.4A7 7 0 0 1 7.6 3.5a7 7 0 1 0 8.9 8.9Z"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinejoin="round"
        />
      </svg>
    ),
  },
  {
    value: "system",
    label: "跟随系统",
    icon: (
      <svg viewBox="0 0 20 20" fill="none" aria-hidden="true" className="size-4">
        <rect x="2.25" y="3" width="15.5" height="10.5" rx="1.5" stroke="currentColor" strokeWidth="1.5" />
        <path d="M7.5 16.5h5M10 13.5v3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
      </svg>
    ),
  },
];

/**
 * 主题切换：与首页内嵌脚本共用 localStorage["theme"] 与
 * <html class="light|dark"> + colorScheme，支持 light / dark / system。
 */
export default function ThemeToggle({ compact = false }: { compact?: boolean }) {
  const [theme, setTheme] = useState<Theme>("system");
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    const stored = localStorage.getItem(STORAGE_KEY) as Theme | null;
    setTheme(stored === "light" || stored === "dark" || stored === "system" ? stored : "system");
    setMounted(true);
  }, []);

  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => {
      if ((localStorage.getItem(STORAGE_KEY) ?? "system") !== "system") return;
      const effective = mq.matches ? "dark" : "light";
      const root = document.documentElement;
      root.classList.remove("light", "dark");
      root.classList.add(effective);
      root.style.colorScheme = effective;
    };
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  const apply = (next: Theme) => {
    const effective =
      next === "system"
        ? window.matchMedia("(prefers-color-scheme: dark)").matches
          ? "dark"
          : "light"
        : next;
    const root = document.documentElement;
    root.classList.remove("light", "dark");
    root.classList.add(effective);
    root.style.colorScheme = effective;
    localStorage.setItem(STORAGE_KEY, next);
    setTheme(next);
  };

  return (
    <div
      role="group"
      aria-label="主题切换"
      className="inline-flex items-center gap-0.5 rounded-md border border-border bg-muted/60 p-0.5"
    >
      {OPTIONS.map((opt) => {
        const active = mounted && theme === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => apply(opt.value)}
            aria-pressed={active}
            title={opt.label}
            className={`focus-ring inline-flex items-center justify-center gap-1.5 rounded-sm px-2 py-1.5 text-2xs font-medium transition-colors duration-quick ease-smooth-out ${
              active
                ? "bg-card text-foreground shadow-card"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {opt.icon}
            {!compact && <span className="hidden sm:inline">{opt.label}</span>}
          </button>
        );
      })}
    </div>
  );
}
