import type { Config } from "tailwindcss";

/**
 * llm-hub 设计系统 · Tailwind 令牌
 *
 * 令牌名逐字取自 data/openrouter-home.html（真实抓取 openrouter.ai 首页）中的 class：
 *   font-brand / text-hero / text-hero-lg / text-hero-xl / text-display / text-section /
 *   text-prose / text-overline / text-button / text-2xs / text-muted-foreground /
 *   text-heading / text-card-foreground / text-foreground / text-primary /
 *   text-info-text / text-positive-text / text-warning-text / text-negative-text /
 *   max-w-7xl / rounded-sm / rounded-md / rounded-lg / rounded-xl / rounded-full /
 *   outline-hidden / focus-visible:shadow-focus / focus-visible:border-focus-border /
 *   duration-micro / duration-quick / duration-fast / duration-very-slow /
 *   ease-smooth-out / ease-in-out-strong / motion-reduce:transition-none /
 *   grid-cols-[7fr_5fr] / bg-warning/12 / border-info/14 / text-muted-foreground/50
 *
 * 颜色一律走 CSS 变量（RGB 三元组），因此支持 `bg-warning/12` 这类透明度修饰符，
 * 并由 :root / .dark 双主题切换。darkMode:"class" 对应首页内嵌主题脚本的
 * document.documentElement.classList.add("light"|"dark")。
 */
const config: Config = {
  darkMode: "class",
  content: [
    "./app/**/*.{ts,tsx}",
    "./components/**/*.{ts,tsx}",
    "./lib/**/*.{ts,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        background: "rgb(var(--background) / <alpha-value>)",
        foreground: "rgb(var(--foreground) / <alpha-value>)",
        heading: "rgb(var(--heading) / <alpha-value>)",
        card: "rgb(var(--card) / <alpha-value>)",
        "card-foreground": "rgb(var(--card-foreground) / <alpha-value>)",
        "card-hover": "rgb(var(--card-hover) / <alpha-value>)",
        muted: "rgb(var(--muted) / <alpha-value>)",
        "muted-foreground": "rgb(var(--muted-foreground) / <alpha-value>)",
        primary: "rgb(var(--primary) / <alpha-value>)",
        "primary-foreground": "rgb(var(--primary-foreground) / <alpha-value>)",
        accent: "rgb(var(--accent) / <alpha-value>)",
        "accent-foreground": "rgb(var(--accent-foreground) / <alpha-value>)",
        border: "rgb(var(--border) / <alpha-value>)",
        input: "rgb(var(--input) / <alpha-value>)",
        "input-bg": "rgb(var(--input-bg) / <alpha-value>)",
        "focus-border": "rgb(var(--focus-border) / <alpha-value>)",
        info: "rgb(var(--info) / <alpha-value>)",
        "info-text": "rgb(var(--info-text) / <alpha-value>)",
        positive: "rgb(var(--positive) / <alpha-value>)",
        "positive-text": "rgb(var(--positive-text) / <alpha-value>)",
        warning: "rgb(var(--warning) / <alpha-value>)",
        "warning-text": "rgb(var(--warning-text) / <alpha-value>)",
        negative: "rgb(var(--negative) / <alpha-value>)",
        "negative-text": "rgb(var(--negative-text) / <alpha-value>)",
      },
      fontFamily: {
        // 真实首页预载字体：gordita_bold-*.woff2（品牌标题）、PlusJakartaSans_VariableFont-*.woff2（正文）
        brand: ["var(--font-brand)"],
        sans: ["var(--font-sans)"],
        mono: ["var(--font-mono)"],
      },
      fontSize: {
        "2xs": ["0.6875rem", { lineHeight: "1rem", letterSpacing: "0.01em" }],
        overline: [
          "0.6875rem",
          { lineHeight: "1rem", letterSpacing: "0.12em", fontWeight: "600" },
        ],
        button: ["0.8125rem", { lineHeight: "1.125rem", fontWeight: "500" }],
        prose: ["1.0625rem", { lineHeight: "1.7" }],
        section: [
          "1.5rem",
          { lineHeight: "1.25", letterSpacing: "-0.02em", fontWeight: "500" },
        ],
        display: [
          "2.25rem",
          { lineHeight: "1.15", letterSpacing: "-0.03em", fontWeight: "700" },
        ],
        hero: [
          "clamp(2.75rem, 7vw, 4.25rem)",
          { lineHeight: "1.03", letterSpacing: "-0.04em", fontWeight: "700" },
        ],
        "hero-lg": [
          "clamp(3.25rem, 8vw, 5rem)",
          { lineHeight: "1.02", letterSpacing: "-0.045em", fontWeight: "700" },
        ],
        "hero-xl": [
          "clamp(3.75rem, 9vw, 5.75rem)",
          { lineHeight: "1.00", letterSpacing: "-0.05em", fontWeight: "700" },
        ],
      },
      // 微圆角：真实首页以 rounded-sm 为主，rounded-md/lg/xl 用于卡片与弹层
      borderRadius: {
        sm: "3px",
        md: "6px",
        lg: "10px",
        xl: "14px",
      },
      boxShadow: {
        // 无障碍焦点环：focus-visible:shadow-focus（双层 ring，forced-colors 下仍可见）
        focus:
          "0 0 0 2px rgb(var(--background)), 0 0 0 4px rgb(var(--focus-border) / 0.9)",
        "focus-soft": "0 0 0 3px rgb(var(--primary) / 0.35)",
        card: "0 1px 2px rgb(var(--foreground) / 0.04), 0 8px 24px -12px rgb(var(--foreground) / 0.10)",
        "card-hover":
          "0 2px 4px rgb(var(--foreground) / 0.05), 0 18px 40px -16px rgb(var(--foreground) / 0.18)",
      },
      transitionDuration: {
        micro: "90ms",
        quick: "150ms",
        fast: "220ms",
        "very-slow": "700ms",
      },
      transitionTimingFunction: {
        "smooth-out": "cubic-bezier(0.22, 1, 0.36, 1)",
        "in-out-strong": "cubic-bezier(0.65, 0, 0.35, 1)",
      },
      keyframes: {
        "fade-up": {
          from: { opacity: "0", transform: "translateY(10px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        "fade-in": { from: { opacity: "0" }, to: { opacity: "1" } },
        "flow-dash": { to: { strokeDashoffset: "-28" } },
        "pulse-soft": {
          "0%,100%": { opacity: "0.45" },
          "50%": { opacity: "1" },
        },
        "bar-grow": {
          from: { transform: "scaleY(0.04)" },
          to: { transform: "scaleY(1)" },
        },
      },
      animation: {
        "fade-up": "fade-up 600ms cubic-bezier(0.22, 1, 0.36, 1) both",
        "fade-in": "fade-in 400ms ease-out both",
        "flow-dash": "flow-dash 1.2s linear infinite",
        "pulse-soft": "pulse-soft 2.4s ease-in-out infinite",
        "bar-grow": "bar-grow 700ms cubic-bezier(0.22, 1, 0.36, 1) both",
      },
      spacing: {
        "0.25": "0.0625rem",
        "18": "4.5rem",
        "22": "5.5rem",
      },
      gridTemplateColumns: {
        "7/5": "7fr 5fr",
        "5/7": "5fr 7fr",
      },
    },
  },
  plugins: [],
};

export default config;
