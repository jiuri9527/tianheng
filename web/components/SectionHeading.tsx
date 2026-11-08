import Link from "next/link";

interface Props {
  id?: string;
  eyebrow?: string;
  title: string;
  description?: string;
  href?: string;
  linkLabel?: string;
  align?: "left" | "center";
}

export default function SectionHeading({
  id,
  eyebrow,
  title,
  description,
  href,
  linkLabel = "查看全部",
  align = "left",
}: Props) {
  return (
    <div
      id={id}
      className={`mb-8 flex flex-col gap-3 md:flex-row md:items-end md:justify-between ${
        align === "center" ? "text-center md:block" : ""
      }`}
    >
      <div>
        {eyebrow && (
          <p className="text-overline uppercase tracking-widest text-primary">{eyebrow}</p>
        )}
        <h2 className="mt-2 font-brand text-section font-medium tracking-tight text-heading md:text-display">
          {title}
        </h2>
        {description && (
          <p className="mt-3 max-w-2xl text-prose text-muted-foreground text-pretty">
            {description}
          </p>
        )}
      </div>
      {href && (
        <Link
          href={href}
          className="focus-ring inline-flex shrink-0 items-center gap-1 rounded-sm text-button font-medium text-primary transition-colors duration-quick hover:text-accent-foreground"
        >
          {linkLabel}
          <svg viewBox="0 0 16 16" fill="none" aria-hidden="true" className="size-3.5">
            <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </Link>
      )}
    </div>
  );
}
