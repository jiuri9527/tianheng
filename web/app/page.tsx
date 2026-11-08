import Link from "next/link";
import { ALL_MODELS, getFeaturedModels, getPriceCompareModels, getRoutingModels } from "@/lib/models";
import { datasetSummary } from "@/lib/stats";
import ModelCard from "@/components/ModelCard";
import RoutingVisual from "@/components/RoutingVisual";
import PriceTable from "@/components/PriceTable";
import DataPolicyVisual from "@/components/DataPolicyVisual";
import SectionHeading from "@/components/SectionHeading";

/**
 * 首页对标 https://openrouter.ai（真实抓取 data/openrouter-home.html）：
 *  h1 "The Unified Interface For Every Model" / "Better prices, better uptime, no subscriptions."
 *  h2 Featured Models · How it works · Model routing visualization ·
 *     Price and Performance · Data policy visualization
 *  lg:grid-cols-[7fr_5fr] · max-w-7xl · font-brand text-hero · text-prose text-muted-foreground
 */
export default function HomePage() {
  const summary = datasetSummary();
  const featured = getFeaturedModels();
  const routing = getRoutingModels();
  const priceModels = getPriceCompareModels();

  const steps = [
    {
      n: "1",
      title: "Signup",
      desc: "创建账户即成为超级用户：所有用户权限相同，没有管理员角色。对应 F1。",
      href: "/register",
    },
    {
      n: "2",
      title: "Contribute a channel",
      desc: "填写 provider_type / base_uri / api_key / visibility，测试连接后提交；自动发现真实模型与参数。对应 F2/F3/F4。",
      href: "/channels",
    },
    {
      n: "3",
      title: "Get your API key",
      desc: "创建 sk- 令牌，用一个 /v1 地址调用池中任意真实模型。对应 F5/F9/F12。",
      href: "/tokens",
    },
  ];

  return (
    <>
      {/* ============ HERO ============ */}
      <section className="relative overflow-hidden border-b border-border">
        <div className="pointer-events-none absolute inset-0 bg-grid opacity-40" aria-hidden="true" />
        <div className="pointer-events-none absolute -right-40 -top-40 size-[36rem] rounded-full bg-primary/8 blur-3xl" aria-hidden="true" />
        <div className="relative mx-auto w-full max-w-7xl px-4 py-20 md:px-6 md:py-28">
          <div className="grid grid-cols-1 gap-10 lg:gap-16 lg:grid-cols-[7fr_5fr]">
            <div className="flex max-w-2xl flex-col gap-5 animate-fade-up">
              <div className="inline-flex w-fit items-center gap-2 rounded-full border border-border bg-card px-3 py-1.5 text-overline text-muted-foreground">
                <span className="size-1.5 animate-pulse-soft rounded-full bg-positive" />
                众包渠道 + 统一出口 · {summary.models} 个真实模型 / {summary.providers} 家供应商
              </div>

              {/* 天衡品牌区（新增；下方 h1 与全部 section / Featured Models / RoutingVisual / PriceTable / DataPolicyVisual 内容原样保留） */}
              <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border border-border bg-card/70 px-4 py-3">
                <span className="font-brand text-lg font-bold tracking-tight text-foreground">天衡</span>
                <span className="font-mono text-overline uppercase tracking-widest text-primary">Tianheng</span>
                <span className="h-4 w-px bg-border" aria-hidden="true" />
                <span className="text-xs leading-relaxed text-muted-foreground">
                  众包渠道 + 统一出口 · 通用大模型 API 聚合中转站
                </span>
              </div>

              <h1
                id="home-hero-heading"
                className="my-0 font-brand text-hero font-bold leading-tight tracking-tight text-foreground sm:text-hero-lg md:text-hero-xl text-balance"
              >
                The Unified Interface For Every Model
              </h1>
              <p className="text-section font-normal text-muted-foreground">
                Better <span className="text-foreground">prices</span>, better{" "}
                <span className="text-foreground">uptime</span>, no subscriptions.
              </p>
              <p className="max-w-xl text-prose text-muted-foreground text-pretty">
                一个平台令牌 <code className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-sm text-foreground">sk-xxxx</code>、
                一个统一地址 <code className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-sm text-foreground">/v1</code>，
                即可调用 OpenAI、Anthropic、Gemini、xAI 与任意 OpenAI 兼容上游聚合而成的全局真实模型池。
              </p>
              <div className="flex flex-wrap gap-3 pt-2">
                <Link
                  href="/tokens"
                  className="focus-ring inline-flex items-center justify-center whitespace-nowrap rounded-md bg-primary px-5 py-2.5 text-button font-medium text-primary-foreground no-underline transition-[filter] duration-quick ease-smooth-out hover:brightness-110"
                >
                  Get API Key
                </Link>
                <Link
                  href="/models"
                  className="focus-ring inline-flex items-center justify-center whitespace-nowrap rounded-md border border-border bg-card px-5 py-2.5 text-button font-medium text-foreground no-underline transition-colors duration-quick ease-smooth-out hover:bg-card-hover"
                >
                  探索 {summary.models} 个模型
                </Link>
              </div>
              <p className="font-mono text-2xs text-muted-foreground/70">
                Fully OpenAI compatible · 数据逐字来自 data/models-real.json
              </p>
            </div>

            {/* 真实数据集统计卡（无编造数值） */}
            <div className="flex flex-col justify-center gap-4">
              <div className="card-surface p-6">
                <p className="text-overline uppercase tracking-widest text-muted-foreground">
                  Global model pool · live dataset
                </p>
                <div className="mt-5 grid grid-cols-2 gap-x-6 gap-y-5">
                  <HeroStat value={summary.models} label="真实模型" />
                  <HeroStat value={summary.providers} label="供应商" />
                  <HeroStat value={summary.freeModels} label="免费模型" />
                  <HeroStat value={summary.multimodalModels} label="多模态模型" />
                  <HeroStat value={summary.toolModels} label="支持工具调用" />
                  <HeroStat value={summary.distinctModalities} label="模态组合" />
                </div>
                <div className="mt-6 border-t border-border pt-4">
                  <p className="font-mono text-2xs text-muted-foreground">max context</p>
                  <p className="mt-1 font-brand text-display font-bold tracking-tight tabular-nums text-heading">
                    {(summary.maxContext / 1_000_000).toFixed(2)}M
                  </p>
                  <Link href={`/models/${encodeURIComponent(summary.maxContextModelId)}`} className="focus-ring mt-1 block truncate rounded-sm font-mono text-2xs text-primary hover:underline">
                    {summary.maxContextModelId} →
                  </Link>
                </div>
              </div>
              <div className="rounded-lg border border-border bg-muted/40 px-5 py-4 font-mono text-2xs leading-relaxed text-muted-foreground">
                source_sha256: {summary.sha256.slice(0, 24)}…
                <br />
                GET https://openrouter.ai/api/v1/models · HTTP 200
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* ============ FEATURED MODELS ============ */}
      <section id="featured" className="mx-auto w-full max-w-7xl px-4 py-20 md:px-6 md:py-24">
        <SectionHeading
          eyebrow="Featured Models"
          title="真实模型，开箱即用"
          description="以下条目逐字来自真实首页 Featured Models 板块与 data/models-real.json；价格、上下文、能力位均取自上游原始 JSON。"
          href="/models"
          linkLabel={`查看全部 ${ALL_MODELS.length} 个`}
        />
        <div className="grid gap-5 md:grid-cols-3">
          {featured.map((m) => <ModelCard key={m.id} model={m} />)}
        </div>
      </section>

      {/* ============ ROUTING VISUALIZATION ============ */}
      <section id="routing" className="border-y border-border bg-muted/25">
        <div className="mx-auto w-full max-w-7xl px-4 py-20 md:px-6 md:py-24">
          <SectionHeading
            eyebrow="Model routing visualization"
            title="一次请求，穿越整个模型池"
            description="客户端 → 统一出口 /v1 → 路由内核 → 协议适配器 → 上游渠道 → 真实模型。加权轮询 / 最少在途 / 优先级 + 熔断 + failover，逐 data block SSE 转发。"
          />
          <RoutingVisual models={routing} />
        </div>
      </section>

      {/* ============ PRICE & PERFORMANCE ============ */}
      <section id="pricing" className="mx-auto w-full max-w-7xl px-4 py-20 md:px-6 md:py-24">
        <SectionHeading
          eyebrow="Price and Performance"
          title="Keep costs in check without sacrificing speed"
          description="真实价格 × 真实上下文长度对照。全部数值由 data/models-real.json 的 pricing / context_length / architecture 字段原样换算，不含编造条目。"
          href="/models?sort=price-asc"
          linkLabel="按价格排序"
        />
        <PriceTable models={priceModels} />
      </section>

      {/* ============ DATA POLICY ============ */}
      <section id="data-policy" className="border-y border-border bg-muted/25">
        <div className="mx-auto w-full max-w-7xl px-4 py-20 md:px-6 md:py-24">
          <SectionHeading
            eyebrow="Data policy visualization"
            title="Custom Data Policies"
            description="Protect your organization with fine grained data policies. 从平台令牌哈希、上游密钥加密、SSRF 防护到日志脱敏，四道闸门贯穿全链路。"
          />
          <DataPolicyVisual />
        </div>
      </section>

      {/* ============ HOW IT WORKS ============ */}
      <section id="how-it-works" className="mx-auto w-full max-w-7xl px-4 py-20 md:px-6 md:py-24">
        <SectionHeading eyebrow="How it works" title="三步接入统一出口" />
        <div className="grid gap-5 md:grid-cols-3">
          {steps.map((s) => (
            <Link key={s.n} href={s.href} className="focus-ring card-surface group flex flex-col gap-4 p-6 transition-[box-shadow,transform] duration-fast ease-smooth-out hover:shadow-card-hover motion-safe:hover:-translate-y-0.5">
              <span className="flex size-10 items-center justify-center rounded-md border border-border bg-muted font-brand text-lg font-bold text-primary">
                {s.n}
              </span>
              <h3 className="font-brand text-lg font-bold tracking-tight text-heading">{s.title}</h3>
              <p className="text-sm leading-relaxed text-muted-foreground">{s.desc}</p>
              <span className="mt-auto inline-flex items-center gap-1 text-button font-medium text-primary">
                前往
                <svg viewBox="0 0 16 16" fill="none" aria-hidden="true" className="size-3.5 transition-transform duration-quick group-hover:translate-x-0.5">
                  <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </span>
            </Link>
          ))}
        </div>
      </section>
    </>
  );
}

function HeroStat({ value, label }: { value: number; label: string }) {
  return (
    <div>
      <dt className="text-overline uppercase tracking-widest text-muted-foreground">{label}</dt>
      <dd className="mt-1 font-brand text-display font-bold leading-tight tracking-tight tabular-nums text-foreground">
        {value}
      </dd>
    </div>
  );
}
