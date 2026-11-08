import Sidebar from "@/components/Sidebar";

interface Props {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
}

/** 控制台统一骨架：top nav（root layout） + Sidebar + 内容区，max-w-7xl 栅格 */
export default function ConsoleLayout({
  eyebrow,
  title,
  description,
  actions,
  children,
}: Props) {
  return (
    <div className="mx-auto w-full max-w-7xl px-4 py-10 md:px-6 md:py-14">
      <div className="flex flex-col gap-10 lg:flex-row lg:gap-12">
        <Sidebar />
        <div className="min-w-0 flex-1">
          <header className="mb-9">
            {eyebrow && (
              <p className="text-overline uppercase tracking-widest text-primary">
                {eyebrow}
              </p>
            )}
            <div className="mt-2 flex flex-wrap items-start justify-between gap-4">
              <div>
                <h1 className="font-brand text-display font-bold leading-tight tracking-tight text-heading">
                  {title}
                </h1>
                {description && (
                  <p className="mt-2 max-w-2xl text-prose text-muted-foreground text-pretty">
                    {description}
                  </p>
                )}
              </div>
              {actions && <div className="flex shrink-0 flex-wrap gap-2">{actions}</div>}
            </div>
          </header>
          {children}
        </div>
      </div>
    </div>
  );
}
