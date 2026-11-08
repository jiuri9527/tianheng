import type { Metadata, Viewport } from "next";
import "./globals.css";
import SiteHeader from "@/components/SiteHeader";
import SiteFooter from "@/components/SiteFooter";

export const metadata: Metadata = {
  title: {
    default: "天衡 Tianheng — The Unified Interface For Every Model",
    template: "%s · 天衡 Tianheng",
  },
  description:
    "天衡（Tianheng）通用大模型 API 聚合中转站：众包渠道 + 统一出口。一个 sk- 令牌、一个 /v1 地址，调用全网真实模型。",
  applicationName: "天衡 Tianheng",
  keywords: ["天衡", "Tianheng", "LLM", "API Gateway", "OpenAI", "Anthropic", "Gemini", "xAI", "模型路由"],
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#FAF9F6" },
    { media: "(prefers-color-scheme: dark)", color: "#0C0D10" },
  ],
};

/**
 * 主题预设脚本：与 data/openrouter-home.html 内嵌脚本同构
 * （真实站点：("class","theme","system",null,["light","dark"],null,true,true)）
 * 在 hydration 前写好 <html class="light|dark"> 与 colorScheme，避免首屏闪白。
 */
const THEME_SCRIPT = `(function(){try{var k="theme",v=localStorage.getItem(k)||"system",e=v==="system"?(window.matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light"):v,r=document.documentElement;r.classList.remove("light","dark");r.classList.add(e);r.style.colorScheme=e;}catch(_){document.documentElement.classList.add("light");}})();`;

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: THEME_SCRIPT }} />
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="anonymous" />
        <link
          rel="stylesheet"
          href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700;800&display=swap"
        />
      </head>
      <body className="min-h-screen bg-background font-sans text-foreground antialiased">
        <a
          href="#main"
          className="focus-ring sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-[100] focus:rounded-md focus:bg-card focus:px-4 focus:py-2 focus:text-button"
        >
          Skip to content
        </a>
        <SiteHeader />
        <main id="main">{children}</main>
        <SiteFooter />
      </body>
    </html>
  );
}
