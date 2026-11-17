import { useEffect, useMemo, useState } from "react";
import { ArrowLeft, Search } from "lucide-react";
import type { HelpTopic } from "../../lib/appnav";
import { useAppNav } from "../../lib/appnav";
import { Alert, Button } from "../form";
import { IconBadge } from "../ui";
import { helpIcon } from "./icons";
import { Markdown, outline } from "./markdown";

export default function HelpPage() {
  const { dest, topics, helpError, reloadHelp, openHelp } = useAppNav();
  const [query, setQuery] = useState("");
  const [sideQuery, setSideQuery] = useState("");
  const topic = topics?.find((item) => item.id === dest.topic) ?? null;

  useEffect(() => {
    if (!topic) {
      window.scrollTo({ top: 0 });
      return;
    }
    if (!dest.heading) {
      window.scrollTo({ top: 0 });
      return;
    }
    const timer = window.setTimeout(() => {
      document.getElementById(dest.heading!)?.scrollIntoView({ behavior: "smooth", block: "start" });
    }, 40);
    return () => window.clearTimeout(timer);
  }, [topic, dest.heading, dest.nonce]);

  if (helpError) {
    return (
      <div className="space-y-4">
        <Header />
        <Alert>{helpError}</Alert>
        <Button onClick={reloadHelp}>Try again</Button>
      </div>
    );
  }

  if (!topics) {
    return (
      <div className="space-y-4">
        <Header />
        <p className="text-sm font-medium text-ink-3">Loading help…</p>
      </div>
    );
  }

  if (!topic) {
    return (
      <Landing
        topics={topics}
        query={query}
        setQuery={setQuery}
        onOpen={(id) => {
          setQuery("");
          openHelp(id);
        }}
      />
    );
  }

  return (
    <Article
      topic={topic}
      topics={topics}
      sideQuery={sideQuery}
      setSideQuery={setSideQuery}
      onOpen={(id) => openHelp(id)}
      onBack={() => openHelp()}
    />
  );
}

function Header() {
  return (
    <header>
      <p className="text-[11px] font-bold uppercase tracking-[0.2em] text-accent">Help</p>
      <h1 className="mt-2 text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">How can we help?</h1>
    </header>
  );
}

function Landing({
  topics,
  query,
  setQuery,
  onOpen,
}: {
  topics: HelpTopic[];
  query: string;
  setQuery: (value: string) => void;
  onOpen: (id: string) => void;
}) {
  const guides = topics.filter((topic) => topic.kind === "guide");
  const popular = topics.filter((topic) => topic.popular && topic.kind === "reference" && !topic.comingSoon);
  const results = useMemo(() => searchTopics(topics, query), [topics, query]);

  return (
    <div className="space-y-10">
      <div className="mx-auto max-w-3xl text-center">
        <p className="text-[11px] font-bold uppercase tracking-[0.2em] text-accent">Help</p>
        <h1 className="mt-2 text-3xl font-extrabold tracking-tight text-ink sm:text-5xl">How can we help?</h1>
        <p className="mx-auto mt-3 max-w-xl text-sm font-semibold leading-relaxed text-ink-2">
          Guides for the jobs you actually do on this server, and a reference page for every feature that is already here.
        </p>
        <SearchField
          value={query}
          onChange={setQuery}
          placeholder="Search guides and articles"
          large
          className="mt-6 text-left"
        />
      </div>

      {query.trim() ? (
        <Results results={results} onOpen={onOpen} />
      ) : (
        <>
          <section>
            <h2 className="mb-4 text-[11px] font-bold uppercase tracking-[0.2em] text-ink-3">Guides</h2>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {guides.map((topic) => (
                <Tile key={topic.id} topic={topic} onOpen={onOpen} />
              ))}
            </div>
          </section>
          {popular.length > 0 && (
            <section>
              <h2 className="mb-4 text-[11px] font-bold uppercase tracking-[0.2em] text-ink-3">Popular articles</h2>
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                {popular.map((topic) => (
                  <ArticleRow key={topic.id} topic={topic} onOpen={onOpen} />
                ))}
              </div>
            </section>
          )}
        </>
      )}
    </div>
  );
}

function Article({
  topic,
  topics,
  sideQuery,
  setSideQuery,
  onOpen,
  onBack,
}: {
  topic: HelpTopic;
  topics: HelpTopic[];
  sideQuery: string;
  setSideQuery: (value: string) => void;
  onOpen: (id: string) => void;
  onBack: () => void;
}) {
  const byId = useMemo(() => new Map(topics.map((item) => [item.id, item])), [topics]);
  const heads = outline(topic.body);
  const relatedGuides = idsOf(topic.related, byId).filter((item) => item.kind === "guide");
  const relatedArticles = [
    ...idsOf(topic.related, byId).filter((item) => item.kind === "reference"),
    ...idsOf(topic.references, byId),
  ].filter((item, index, all) => all.findIndex((other) => other.id === item.id) === index && item.id !== topic.id);

  return (
    <div className="lg:grid lg:grid-cols-[16rem_minmax(0,1fr)] lg:items-start lg:gap-8">
      <aside className="mb-6 lg:sticky lg:top-6 lg:mb-0 lg:max-h-[calc(100vh-3rem)] lg:overflow-y-auto lg:pr-1">
        <button
          type="button"
          onClick={onBack}
          className="mb-4 inline-flex items-center gap-1.5 text-xs font-bold text-ink-2 hover:text-accent"
        >
          <ArrowLeft size={14} />
          All guides
        </button>
        <details className="rounded-2xl border border-line bg-surface-2 px-3 py-2 lg:hidden">
          <summary className="cursor-pointer text-xs font-bold text-ink">Topics</summary>
          <div className="mt-3">
            <SearchField value={sideQuery} onChange={setSideQuery} placeholder="Search topics" />
            <TopicNav topics={topics} query={sideQuery} current={topic.id} onOpen={onOpen} />
          </div>
        </details>
        <div className="hidden lg:block">
          <SearchField value={sideQuery} onChange={setSideQuery} placeholder="Search topics" />
          <TopicNav topics={topics} query={sideQuery} current={topic.id} onOpen={onOpen} />
        </div>
      </aside>
      <article className="min-w-0">
        <p className="text-[11px] font-bold uppercase tracking-[0.18em] text-ink-3">
          {topic.kind === "guide" ? "Guide" : "Reference"}
        </p>
        <div className="mt-2 flex flex-wrap items-center gap-3">
          <h1 className="text-3xl font-extrabold tracking-tight text-ink sm:text-4xl">{topic.title}</h1>
          {topic.comingSoon && <Soon />}
        </div>
        <p className="mt-3 max-w-2xl text-sm font-semibold leading-relaxed text-ink-2">{topic.summary}</p>
        {heads.length > 1 && (
          <nav className="mt-5 rounded-2xl border border-line bg-surface-2 px-4 py-3" aria-label="On this page">
            <p className="text-[10px] font-bold uppercase tracking-[0.16em] text-ink-3">On this page</p>
            <ul className="mt-2 space-y-1">
              {heads.map((head) => (
                <li key={head.id} className={head.level === 3 ? "pl-3" : ""}>
                  <a
                    href={`#${head.id}`}
                    onClick={(e) => {
                      e.preventDefault();
                      document.getElementById(head.id)?.scrollIntoView({ behavior: "smooth", block: "start" });
                    }}
                    className="text-xs font-bold text-ink-2 hover:text-accent"
                  >
                    {head.text}
                  </a>
                </li>
              ))}
            </ul>
          </nav>
        )}
        <Markdown source={topic.body} />
        {relatedGuides.length > 0 && (
          <LinkList title="Related guides" topics={relatedGuides} onOpen={onOpen} />
        )}
        {relatedArticles.length > 0 && (
          <LinkList title={topic.kind === "guide" ? "In the reference" : "Related articles"} topics={relatedArticles} onOpen={onOpen} />
        )}
      </article>
    </div>
  );
}

function TopicNav({
  topics,
  query,
  current,
  onOpen,
}: {
  topics: HelpTopic[];
  query: string;
  current: string;
  onOpen: (id: string) => void;
}) {
  const shown = query.trim() ? searchTopics(topics, query).map((hit) => hit.topic) : topics;
  const guides = shown.filter((topic) => topic.kind === "guide");
  const refs = shown.filter((topic) => topic.kind === "reference");
  return (
    <div className="mt-4 space-y-4">
      <NavGroup label="Guides" topics={guides} current={current} onOpen={onOpen} />
      <NavGroup label="Reference" topics={refs} current={current} onOpen={onOpen} />
      {shown.length === 0 && <p className="text-xs font-medium text-ink-3">No topics match.</p>}
    </div>
  );
}

function NavGroup({
  label,
  topics,
  current,
  onOpen,
}: {
  label: string;
  topics: HelpTopic[];
  current: string;
  onOpen: (id: string) => void;
}) {
  if (topics.length === 0) return null;
  return (
    <div>
      <p className="px-2 text-[10px] font-bold uppercase tracking-[0.16em] text-ink-3">{label}</p>
      <ul className="mt-1">
        {topics.map((topic) => {
          const on = topic.id === current;
          return (
            <li key={topic.id}>
              <button
                type="button"
                onClick={() => onOpen(topic.id)}
                aria-current={on ? "page" : undefined}
                className={`flex w-full items-center gap-2 rounded-xl px-2 py-1.5 text-left text-xs font-bold ${
                  on ? "bg-accent/10 text-accent" : "text-ink-2 hover:bg-accent/8 hover:text-ink"
                }`}
              >
                <span className="min-w-0 flex-1 truncate">{topic.title}</span>
                {topic.comingSoon && <span className="shrink-0 text-[9px] uppercase tracking-wider text-ink-3">Soon</span>}
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function Tile({ topic, onOpen }: { topic: HelpTopic; onOpen: (id: string) => void }) {
  return (
    <button
      type="button"
      onClick={() => onOpen(topic.id)}
      className="glass flex h-full flex-col items-start rounded-3xl p-5 text-left transition hover:-translate-y-0.5"
    >
      <div className="flex w-full items-start justify-between gap-3">
        <IconBadge icon={helpIcon(topic.icon)} size={48} muted={topic.comingSoon} />
        {topic.comingSoon && <Soon />}
      </div>
      <h3 className="mt-4 text-base font-extrabold text-ink">{topic.title}</h3>
      <p className="mt-1.5 text-sm font-medium leading-relaxed text-ink-2">{topic.summary}</p>
    </button>
  );
}

function ArticleRow({ topic, onOpen }: { topic: HelpTopic; onOpen: (id: string) => void }) {
  return (
    <button
      type="button"
      onClick={() => onOpen(topic.id)}
      className="glass flex items-center gap-4 rounded-3xl p-4 text-left transition hover:-translate-y-0.5"
    >
      <IconBadge icon={helpIcon(topic.icon)} size={44} />
      <span className="min-w-0">
        <span className="block truncate text-sm font-extrabold text-ink">{topic.title}</span>
        <span className="mt-0.5 block text-xs font-medium leading-relaxed text-ink-3">{topic.summary}</span>
      </span>
    </button>
  );
}

function Results({
  results,
  onOpen,
}: {
  results: { topic: HelpTopic; snippet: string }[];
  onOpen: (id: string) => void;
}) {
  if (results.length === 0) {
    return <p className="text-sm font-semibold text-ink-2">Nothing in Help matches that.</p>;
  }
  return (
    <ul className="space-y-3">
      {results.map(({ topic, snippet }) => (
        <li key={topic.id}>
          <button
            type="button"
            onClick={() => onOpen(topic.id)}
            className="glass w-full rounded-3xl p-4 text-left"
          >
            <span className="flex items-center gap-2">
              <span className="text-[10px] font-bold uppercase tracking-[0.16em] text-ink-3">
                {topic.kind === "guide" ? "Guide" : "Article"}
              </span>
              {topic.comingSoon && <Soon />}
            </span>
            <span className="mt-1 block text-base font-extrabold text-ink">{topic.title}</span>
            <span className="mt-1 block text-sm font-medium leading-relaxed text-ink-2">{snippet}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

function LinkList({
  title,
  topics,
  onOpen,
}: {
  title: string;
  topics: HelpTopic[];
  onOpen: (id: string) => void;
}) {
  return (
    <section className="mt-10">
      <h2 className="text-[11px] font-bold uppercase tracking-[0.2em] text-ink-3">{title}</h2>
      <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
        {topics.map((topic) => (
          <ArticleRow key={topic.id} topic={topic} onOpen={onOpen} />
        ))}
      </div>
    </section>
  );
}

function Soon() {
  return (
    <span className="rounded-full bg-track px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider text-ink-3">
      Coming soon
    </span>
  );
}

function SearchField({
  value,
  onChange,
  placeholder,
  large = false,
  className = "",
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  large?: boolean;
  className?: string;
}) {
  return (
    <div className={`relative ${className}`}>
      <Search
        size={large ? 18 : 15}
        className="pointer-events-none absolute left-4 top-1/2 -translate-y-1/2 text-ink-3"
      />
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        aria-label={placeholder}
        className={`w-full rounded-2xl border border-line bg-surface-2 font-semibold text-ink placeholder:text-ink-3 ${
          large ? "py-4 pl-12 pr-4 text-base shadow-lg shadow-accent/5" : "py-2.5 pl-10 pr-3 text-xs"
        }`}
      />
    </div>
  );
}

function idsOf(ids: string[] | null | undefined, byId: Map<string, HelpTopic>): HelpTopic[] {
  return (ids ?? []).map((id) => byId.get(id)).filter((topic): topic is HelpTopic => Boolean(topic));
}

function searchTopics(topics: HelpTopic[], query: string): { topic: HelpTopic; snippet: string }[] {
  const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return [];
  const hits: { topic: HelpTopic; snippet: string }[] = [];
  for (const topic of topics) {
    const plain = `${topic.title}\n${topic.summary}\n${strip(topic.body)}`;
    const hay = plain.toLowerCase();
    if (!terms.every((term) => hay.includes(term))) continue;
    hits.push({ topic, snippet: snippetAround(plain, terms[0]) });
  }
  return hits;
}

function strip(markdown: string): string {
  return markdown
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/\[\[(?:route|help):[^|]+\|([^\]]+)\]\]/g, "$1")
    .replace(/[#>*`|_-]/g, " ")
    .replace(/\s+/g, " ");
}

function snippetAround(text: string, term: string): string {
  const hay = text.toLowerCase();
  const at = hay.indexOf(term);
  if (at < 0) return text.slice(0, 160);
  const start = Math.max(0, at - 60);
  const slice = text.slice(start, start + 180).trim();
  return `${start > 0 ? "…" : ""}${slice}${start + 180 < text.length ? "…" : ""}`;
}
