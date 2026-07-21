// Three variants of the Curator publishing workflow, switchable via ?variant=, on a throwaway single-page prototype.
import { useEffect, useId, useMemo, useState } from "react";
import {
  AlertTriangle,
  ArrowLeft,
  ArrowRight,
  Bell,
  Check,
  ChevronDown,
  ChevronRight,
  CircleHelp,
  Clock3,
  Eye,
  GalleryHorizontalEnd,
  GitMerge,
  Grid2X2,
  ImagePlus,
  Inbox,
  Info,
  LayoutDashboard,
  ListFilter,
  Menu,
  MessageSquare,
  Moon,
  MoreHorizontal,
  MoveRight,
  PanelLeftClose,
  ContactRound,
  Plus,
  RefreshCw,
  Search,
  Settings,
  ShieldCheck,
  Sparkles,
  Split,
  Sun,
  Undo2,
  Upload,
  UserRoundCheck,
  UsersRound,
  WandSparkles,
  X,
} from "lucide-react";
import { Avatar, AvatarFallback, AvatarImage, Badge, Button, Card, Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./components/ui";
import { cn } from "./lib/utils";

type VariantKey = "A" | "B" | "C";
type AccentKey = "cyan" | "sky" | "blue";
type AppView = "inbox" | "events" | "people" | "activity";
type EditorSection = "media" | "moments" | "attendance" | "audiences" | "review";

type Person = {
  name: string;
  initials: string;
  image: string;
  reasons: Array<{ label: string; tone: "blue" | "green" | "purple" | "red" }>;
  detail: string;
  included: boolean;
};

const photos = [
  { src: "https://images.unsplash.com/photo-1490750967868-88aa4486c946?auto=format&fit=crop&w=900&q=80", alt: "Wildflowers in a garden" },
  { src: "https://images.unsplash.com/photo-1500530855697-b586d89ba3ee?auto=format&fit=crop&w=900&q=80", alt: "Family picnic landscape" },
  { src: "https://images.unsplash.com/photo-1533488765986-dfa2a9939acd?auto=format&fit=crop&w=900&q=80", alt: "Children outdoors" },
  { src: "https://images.unsplash.com/photo-1529156069898-49953e39b3ac?auto=format&fit=crop&w=900&q=80", alt: "Family gathering" },
  { src: "https://images.unsplash.com/photo-1499209974431-9dddcece7f88?auto=format&fit=crop&w=900&q=80", alt: "Summer field" },
  { src: "https://images.unsplash.com/photo-1504151932400-72d4384f04b3?auto=format&fit=crop&w=900&q=80", alt: "Parent and child" },
  { src: "https://images.unsplash.com/photo-1609220136736-443140cffec6?auto=format&fit=crop&w=900&q=80", alt: "Family walking together" },
  { src: "https://images.unsplash.com/photo-1474552226712-ac0f0961a954?auto=format&fit=crop&w=900&q=80", alt: "Couple outdoors" },
];

const people: Person[] = [
  {
    name: "Maya Chen",
    initials: "MC",
    image: "https://i.pravatar.cc/96?img=47",
    reasons: [
      { label: "Present", tone: "green" },
      { label: "Interested in Leo", tone: "blue" },
    ],
    detail: "Maya is confirmed in this Moment. Her Interest list also matches Leo Chen.",
    included: true,
  },
  {
    name: "Jordan Lee",
    initials: "JL",
    image: "https://i.pravatar.cc/96?img=12",
    reasons: [{ label: "Interested in Maya + 2", tone: "blue" }],
    detail: "Jordan is interested in Maya Chen, Leo Chen, and Nora Lee, who are confirmed present.",
    included: true,
  },
  {
    name: "Priya Shah",
    initials: "PS",
    image: "https://i.pravatar.cc/96?img=32",
    reasons: [{ label: "Manually included", tone: "purple" }],
    detail: "You manually included Priya. No current Attendance or Interest-list match applies.",
    included: true,
  },
  {
    name: "Eli Chen",
    initials: "EC",
    image: "https://i.pravatar.cc/96?img=5",
    reasons: [
      { label: "Present", tone: "green" },
      { label: "Manually excluded", tone: "red" },
    ],
    detail: "Eli is confirmed present, but you manually excluded him from this Audience proposal.",
    included: false,
  },
];

const queue = [
  { icon: AlertTriangle, tone: "red" as const, title: "3 source files unavailable", meta: "Lake Weekend · delivery stopped", action: "Review now" },
  { icon: ShieldCheck, tone: "purple" as const, title: "Summer reunion ready to review", meta: "4 Moments · 86 Media items", action: "Continue review" },
  { icon: RefreshCw, tone: "amber" as const, title: "18 staged changes", meta: "Grandma's 80th · updated 2 hours ago", action: "Review update" },
  { icon: ImagePlus, tone: "blue" as const, title: "2 new Source albums", meta: "Discovered from Immich", action: "Triage" },
];

const moments = [
  { title: "Friday arrival", date: "Jun 14", items: 18, people: "Maya, Eli, Leo + 2", audience: "7 recipients", color: "green" as const },
  { title: "Saturday picnic", date: "Jun 15", items: 42, people: "Maya, Nora, Leo + 8", audience: "12 recipients", color: "blue" as const },
  { title: "After the picnic", date: "Jun 15", items: 9, people: "Nora, Robin", audience: "Curator only", color: "neutral" as const },
  { title: "Sunday breakfast", date: "Jun 16", items: 17, people: "Jordan, Priya, Leo + 3", audience: "8 recipients", color: "purple" as const },
];

const variantNames: Record<VariantKey, string> = {
  A: "Guided work queue",
  B: "Split-pane command center",
  C: "Event canvas",
};

function useVariant() {
  const read = (): VariantKey => {
    const value = new URLSearchParams(window.location.search).get("variant")?.toUpperCase();
    return value === "A" || value === "C" ? value : "B";
  };
  const [variant, setVariantState] = useState<VariantKey>(read);

  const setVariant = (next: VariantKey) => {
    const params = new URLSearchParams(window.location.search);
    params.set("variant", next);
    window.history.replaceState(null, "", `${window.location.pathname}?${params.toString()}`);
    setVariantState(next);
  };

  useEffect(() => {
    const onPopState = () => setVariantState(read());
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  return { variant, setVariant };
}

function useTheme() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains("dark"));
  const toggle = () => {
    document.documentElement.classList.toggle("dark");
    setDark((value) => !value);
  };
  return { dark, toggle };
}

function useAccent(dark: boolean) {
  const read = (): AccentKey => {
    const value = new URLSearchParams(window.location.search).get("accent");
    return value === "cyan" || value === "blue" ? value : "sky";
  };
  const [accent, setAccentState] = useState<AccentKey>(read);

  const setAccent = (next: AccentKey) => {
    const params = new URLSearchParams(window.location.search);
    params.set("accent", next);
    window.history.replaceState(null, "", `${window.location.pathname}?${params.toString()}`);
    setAccentState(next);
  };

  useEffect(() => {
    const palettes = {
      cyan: dark ? { primary: "#22d3ee", foreground: "#083344", secondary: "#164e63", secondaryForeground: "#a5f3fc", ring: "#22d3ee" } : { primary: "#0891b2", foreground: "#ffffff", secondary: "#cffafe", secondaryForeground: "#155e75", ring: "#06b6d4" },
      sky: dark ? { primary: "#38bdf8", foreground: "#082f49", secondary: "#0c4a6e", secondaryForeground: "#bae6fd", ring: "#38bdf8" } : { primary: "#0284c7", foreground: "#ffffff", secondary: "#e0f2fe", secondaryForeground: "#075985", ring: "#0ea5e9" },
      blue: dark ? { primary: "#60a5fa", foreground: "#172554", secondary: "#1e3a8a", secondaryForeground: "#bfdbfe", ring: "#60a5fa" } : { primary: "#2563eb", foreground: "#ffffff", secondary: "#dbeafe", secondaryForeground: "#1e40af", ring: "#3b82f6" },
    } as const;
    const palette = palettes[accent];
    const root = document.documentElement;
    root.style.setProperty("--primary", palette.primary);
    root.style.setProperty("--primary-foreground", palette.foreground);
    root.style.setProperty("--secondary", palette.secondary);
    root.style.setProperty("--secondary-foreground", palette.secondaryForeground);
    root.style.setProperty("--ring", palette.ring);
  }, [accent, dark]);

  return { accent, setAccent };
}

function PrototypeSwitcher({ variant, setVariant, accent, setAccent }: { variant: VariantKey; setVariant: (value: VariantKey) => void; accent: AccentKey; setAccent: (value: AccentKey) => void }) {
  const variants: VariantKey[] = ["A", "B", "C"];
  const cycle = (direction: -1 | 1) => {
    const current = variants.indexOf(variant);
    setVariant(variants[(current + direction + variants.length) % variants.length]);
  };

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement;
      if (target.matches("input, textarea, [contenteditable='true']")) return;
      if (event.key === "ArrowLeft") cycle(-1);
      if (event.key === "ArrowRight") cycle(1);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  });

  if (import.meta.env.PROD) return null;
  return (
    <div className="fixed bottom-4 left-1/2 z-[100] -translate-x-1/2 rounded-2xl border border-white/15 bg-zinc-950 p-1.5 text-white shadow-2xl shadow-black/40">
      <div className="flex items-center">
        <button className="rounded-full p-2 hover:bg-white/15" onClick={() => cycle(-1)} aria-label="Previous variant"><ArrowLeft className="size-4" /></button>
        <div className="min-w-52 px-3 text-center text-xs font-semibold"><span className="text-sky-300">{variant}</span> · {variantNames[variant]}</div>
        <button className="rounded-full p-2 hover:bg-white/15" onClick={() => cycle(1)} aria-label="Next variant"><ArrowRight className="size-4" /></button>
      </div>
      <div className="flex items-center justify-center gap-1 border-t border-white/10 pt-1.5">
        {(["cyan", "sky", "blue"] as AccentKey[]).map((color) => (
          <button key={color} onClick={() => setAccent(color)} className={cn("flex items-center gap-1.5 rounded-full px-2 py-1 text-[10px] font-semibold capitalize", accent === color ? "bg-white/15 text-white" : "text-white/55 hover:text-white")}>
            <span className={cn("size-2.5 rounded-full", color === "cyan" ? "bg-cyan-500" : color === "sky" ? "bg-sky-500" : "bg-blue-500")} />{color}
          </button>
        ))}
      </div>
    </div>
  );
}

function MementoMark({ className }: { className?: string }) {
  const id = useId();
  const leftId = `${id}-memento-left`;
  const rightId = `${id}-memento-right`;
  const heroId = `${id}-memento-hero`;
  return (
    <svg className={className} viewBox="160 200 704 640" role="img" aria-label="Memento">
      {/* Mirrors design/app-icon/memento-icon-dark.svg. Keep the geometry in sync with the master icon. */}
      <defs>
        <linearGradient id={leftId} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--primary)" stopOpacity=".78" /><stop offset="1" stopColor="var(--primary)" stopOpacity=".62" /></linearGradient>
        <linearGradient id={rightId} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--primary)" /><stop offset="1" stopColor="var(--primary)" stopOpacity=".82" /></linearGradient>
        <linearGradient id={heroId} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--secondary-foreground)" /><stop offset="1" stopColor="var(--primary)" /></linearGradient>
      </defs>
      <rect x="246" y="270" width="410" height="500" rx="112" fill={`url(#${leftId})`} transform="rotate(-15 451 520)" />
      <rect x="368" y="270" width="410" height="500" rx="112" fill={`url(#${rightId})`} transform="rotate(15 573 520)" />
      <rect x="322" y="282" width="380" height="500" rx="106" fill={`url(#${heroId})`} />
    </svg>
  );
}

function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <div className="flex items-center gap-3">
      <MementoMark className="size-9 shrink-0" />
      {!compact && <span className="text-lg font-bold tracking-tight">Memento</span>}
    </div>
  );
}

function ThemeButton({ dark, toggle }: { dark: boolean; toggle: () => void }) {
  return <Button variant="ghost" size="icon" onClick={toggle} aria-label="Toggle color theme">{dark ? <Sun className="size-4" /> : <Moon className="size-4" />}</Button>;
}

function HeaderActions({ dark, toggle }: { dark: boolean; toggle: () => void }) {
  return (
    <div className="flex items-center gap-1">
      <Button variant="ghost" size="icon"><Search className="size-4" /></Button>
      <Button variant="ghost" size="icon" className="relative"><Bell className="size-4" /><span className="absolute right-2 top-2 size-2 rounded-full bg-blue-500" /></Button>
      <ThemeButton dark={dark} toggle={toggle} />
      <Avatar className="ml-1 block size-9 overflow-hidden rounded-full"><AvatarImage src="https://i.pravatar.cc/96?img=11" /><AvatarFallback>RJ</AvatarFallback></Avatar>
    </div>
  );
}

function PhotoStrip({ offset = 0, className }: { offset?: number; className?: string }) {
  return (
    <div className={cn("grid grid-cols-4 gap-1 overflow-hidden rounded-xl", className)}>
      {photos.slice(offset, offset + 4).map((photo) => <img key={photo.src} src={photo.src} alt={photo.alt} className="aspect-square size-full object-cover" />)}
    </div>
  );
}

function PublishDialog({ children }: { children: React.ReactNode }) {
  return (
    <Dialog>
      <DialogTrigger asChild>{children}</DialogTrigger>
      <DialogContent>
        <div className="mb-5 flex size-12 items-center justify-center rounded-full bg-blue-500/15 text-blue-500"><Upload className="size-6" /></div>
        <DialogTitle className="text-2xl font-bold">Publish Summer reunion?</DialogTitle>
        <DialogDescription className="mt-2 text-muted-foreground">This publishes the complete reviewed Event draft to its approved Audiences.</DialogDescription>
        <div className="my-6 grid gap-3 sm:grid-cols-3">
          <Card className="p-4"><div className="text-2xl font-bold">86</div><div className="text-xs text-muted-foreground">Media items</div></Card>
          <Card className="p-4"><div className="text-2xl font-bold">4</div><div className="text-xs text-muted-foreground">Moments reviewed</div></Card>
          <Card className="p-4"><div className="text-2xl font-bold">12</div><div className="text-xs text-muted-foreground">Recipients at most</div></Card>
        </div>
        <div className="rounded-2xl bg-muted p-4 text-sm">
          <div className="flex items-start gap-3"><Eye className="mt-0.5 size-4 text-muted-foreground" /><div><strong>One curator-only Moment</strong><p className="mt-1 text-muted-foreground">“After the picnic” has an empty Audience. It will remain visible only to you.</p></div></div>
        </div>
        <div className="mt-6 flex justify-end gap-2"><Button variant="ghost">Keep editing</Button><Button><Upload className="size-4" />Publish Event</Button></div>
      </DialogContent>
    </Dialog>
  );
}

function AudiencePerson({ person, compact = false }: { person: Person; compact?: boolean }) {
  const [included, setIncluded] = useState(person.included);
  return (
    <div className={cn("flex items-start gap-3", compact ? "py-2" : "rounded-2xl border border-border p-4")}>
      <Avatar className="block size-10 shrink-0 overflow-hidden rounded-full"><AvatarImage src={person.image} /><AvatarFallback>{person.initials}</AvatarFallback></Avatar>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2"><span className="font-semibold">{person.name}</span>{!included && <Badge tone="red">Excluded</Badge>}</div>
        <div className="mt-1.5 flex flex-wrap gap-1.5">{person.reasons.map((reason) => <Badge key={reason.label} tone={reason.tone}>{reason.label}</Badge>)}</div>
        {!compact && <p className="mt-2 text-xs leading-5 text-muted-foreground">{person.detail}</p>}
      </div>
      <button
        onClick={() => setIncluded((value) => !value)}
        className={cn("relative mt-1 h-6 w-11 shrink-0 rounded-full transition-colors", included ? "bg-primary" : "bg-muted")}
        aria-label={`${included ? "Exclude" : "Include"} ${person.name}`}
      ><span className={cn("absolute top-1 size-4 rounded-full bg-white transition-all", included ? "left-6" : "left-1")} /></button>
    </div>
  );
}

function AudiencePanel({ compact = false }: { compact?: boolean }) {
  const includedCount = people.filter((person) => person.included).length;
  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <div><h3 className="font-bold">Audience proposal</h3><p className="text-xs text-muted-foreground">{includedCount} included · 1 excluded</p></div>
        <Button size="sm" variant="outline"><Plus className="size-3" />Add</Button>
      </div>
      <div className={cn("space-y-2", compact && "divide-y divide-border space-y-0")}>{people.map((person) => <AudiencePerson key={person.name} person={person} compact={compact} />)}</div>
      <button className="mt-3 flex w-full items-center justify-center gap-2 rounded-xl p-2 text-xs font-semibold text-muted-foreground hover:bg-accent"><Eye className="size-3.5" />View full Interest lists and audit details</button>
    </div>
  );
}

function MomentList({ selected, onSelect, dense = false }: { selected?: number; onSelect?: (index: number) => void; dense?: boolean }) {
  return (
    <div className="space-y-3">
      {moments.map((moment, index) => (
        <button key={moment.title} onClick={() => onSelect?.(index)} className={cn("w-full text-left transition-colors", dense ? "border-b border-border px-3 py-3" : "rounded-2xl border border-border p-4", selected === index ? "border-primary bg-primary/10" : "hover:bg-accent/60")}>
          <div className="flex items-start justify-between gap-3">
            <div><div className="flex items-center gap-2"><span className="font-semibold">{moment.title}</span>{index === 2 && <Badge>Curator only</Badge>}</div><p className="mt-1 text-xs text-muted-foreground">{moment.date} · {moment.items} items</p></div>
            <ChevronRight className="size-4 text-muted-foreground" />
          </div>
          <div className="mt-3 flex flex-wrap gap-2"><Badge tone="green"><UserRoundCheck className="size-3" />{moment.people}</Badge><Badge tone={moment.color}><UsersRound className="size-3" />{moment.audience}</Badge></div>
        </button>
      ))}
    </div>
  );
}

function PeopleMatrix() {
  const rows = [
    { name: "Maya Chen", avatar: "https://i.pravatar.cc/96?img=47", circles: [true, true, false], impact: "Visible to 8 recipients" },
    { name: "Jordan Lee", avatar: "https://i.pravatar.cc/96?img=12", circles: [false, true, true], impact: "Visible to 11 recipients" },
    { name: "Priya Shah", avatar: "https://i.pravatar.cc/96?img=32", circles: [false, false, true], impact: "Visible to 5 recipients" },
    { name: "Leo Chen", avatar: "https://i.pravatar.cc/96?img=7", circles: [true, true, false], impact: "Visible to 8 recipients" },
  ];
  return (
    <div className="overflow-hidden rounded-2xl border border-border">
      <div className="grid grid-cols-[minmax(180px,1fr)_repeat(3,90px)] bg-muted px-4 py-3 text-xs font-semibold text-muted-foreground"><span>Person</span><span className="text-center">Chen</span><span className="text-center">Local</span><span className="text-center">Shah</span></div>
      {rows.map((row) => <div key={row.name} className="grid grid-cols-[minmax(180px,1fr)_repeat(3,90px)] items-center border-t border-border px-4 py-3"><div className="flex items-center gap-3"><Avatar className="size-9 overflow-hidden rounded-full"><AvatarImage src={row.avatar} /><AvatarFallback>{row.name[0]}</AvatarFallback></Avatar><div><div className="text-sm font-semibold">{row.name}</div><div className="text-xs text-muted-foreground">{row.impact}</div></div></div>{row.circles.map((active, index) => <div key={index} className="grid place-items-center"><button className={cn("grid size-7 place-items-center rounded-lg border", active ? "border-primary bg-primary text-primary-foreground" : "border-border hover:bg-accent")}>{active && <Check className="size-4" />}</button></div>)}</div>)}
    </div>
  );
}

function VariantA({ dark, toggle }: { dark: boolean; toggle: () => void }) {
  const [view, setView] = useState<AppView>("inbox");
  const [section, setSection] = useState<EditorSection>("moments");
  const nav = [
    { id: "inbox" as const, label: "Inbox", icon: Inbox, count: 7 },
    { id: "events" as const, label: "Events", icon: GalleryHorizontalEnd },
    { id: "people" as const, label: "People", icon: ContactRound },
    { id: "activity" as const, label: "Activity", icon: Clock3 },
  ];
  return (
    <div className="min-h-screen bg-background pb-24">
      <aside className="fixed inset-y-0 left-0 z-20 hidden w-60 border-r border-border bg-card p-5 lg:block">
        <Brand />
        <nav className="mt-10 space-y-1">{nav.map((item) => <button key={item.id} onClick={() => setView(item.id)} className={cn("flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-sm font-semibold", view === item.id ? "bg-primary/15 text-primary" : "text-muted-foreground hover:bg-accent hover:text-foreground")}><item.icon className="size-4" /><span className="flex-1 text-left">{item.label}</span>{item.count && <Badge tone="blue">{item.count}</Badge>}</button>)}</nav>
        <div className="absolute inset-x-5 bottom-5"><button className="flex w-full items-center gap-3 rounded-xl p-3 hover:bg-accent"><Avatar className="size-9 overflow-hidden rounded-full"><AvatarImage src="https://i.pravatar.cc/96?img=11" /><AvatarFallback>RJ</AvatarFallback></Avatar><div className="text-left"><div className="text-sm font-semibold">Robin</div><div className="text-xs text-muted-foreground">Curator</div></div><Settings className="ml-auto size-4 text-muted-foreground" /></button></div>
      </aside>
      <header className="sticky top-0 z-10 flex h-16 items-center justify-between border-b border-border bg-background/90 px-4 backdrop-blur-xl lg:ml-60 lg:px-8"><div className="lg:hidden"><Brand /></div><div className="hidden text-sm text-muted-foreground lg:block">All changes saved</div><HeaderActions dark={dark} toggle={toggle} /></header>
      <main className="mx-auto max-w-7xl px-4 py-7 lg:ml-60 lg:px-8">
        {view === "inbox" && <div>
          <div className="mb-8"><Badge tone="blue"><Sparkles className="size-3" />7 items need attention</Badge><h1 className="mt-3 text-3xl font-bold tracking-tight sm:text-4xl">Good afternoon, Robin</h1><p className="mt-2 text-muted-foreground">Start with privacy and delivery issues, then keep publishing.</p></div>
          <div className="grid gap-8 xl:grid-cols-[minmax(0,1fr)_380px]">
            <div><div className="mb-3 flex items-center justify-between"><h2 className="font-bold">Your work queue</h2><Button size="sm" variant="ghost"><ListFilter className="size-4" />Filter</Button></div><div className="space-y-3">{queue.map((item, index) => <Card key={item.title} className={cn("flex flex-col gap-4 p-4 sm:flex-row sm:items-center", index === 0 && "border-red-500/35")}><div className={cn("grid size-11 shrink-0 place-items-center rounded-xl", item.tone === "red" ? "bg-red-500/15 text-red-500" : item.tone === "amber" ? "bg-amber-500/15 text-amber-500" : item.tone === "purple" ? "bg-violet-500/15 text-violet-500" : "bg-blue-500/15 text-blue-500")}><item.icon className="size-5" /></div><div className="min-w-0 flex-1"><h3 className="font-semibold">{item.title}</h3><p className="mt-1 text-sm text-muted-foreground">{item.meta}</p></div><Button variant={index === 1 ? "default" : "outline"} size="sm" onClick={() => { setView("events"); setSection(index === 2 ? "review" : "moments"); }}>{item.action}<ChevronRight className="size-4" /></Button></Card>)}</div>
            <Card className="mt-8 p-5"><div className="flex items-center justify-between"><div><h2 className="font-bold">New from Immich</h2><p className="text-sm text-muted-foreground">Triage individually or select several</p></div><Button size="sm" variant="outline">Select</Button></div><div className="mt-4 grid grid-cols-2 gap-3"><div className="overflow-hidden rounded-xl border border-border"><img src={photos[6].src} alt={photos[6].alt} className="aspect-video w-full object-cover" /><div className="p-3"><div className="text-sm font-semibold">School picnic</div><div className="text-xs text-muted-foreground">34 items</div><div className="mt-3 flex gap-1"><Button size="sm" className="flex-1">Draft</Button><Button size="sm" variant="ghost">Ignore</Button></div></div></div><div className="overflow-hidden rounded-xl border border-border"><img src={photos[4].src} alt={photos[4].alt} className="aspect-video w-full object-cover" /><div className="p-3"><div className="text-sm font-semibold">Garden</div><div className="text-xs text-muted-foreground">12 items</div><div className="mt-3 flex gap-1"><Button size="sm" className="flex-1">Draft</Button><Button size="sm" variant="ghost">Ignore</Button></div></div></div></div></Card></div>
            <div><Card className="sticky top-24 overflow-hidden"><div className="relative"><img src={photos[3].src} alt={photos[3].alt} className="h-52 w-full object-cover" /><div className="photo-fade absolute inset-0" /><div className="absolute bottom-4 left-4 text-white"><Badge className="mb-2 bg-white/15 text-white">Draft Event</Badge><h2 className="text-2xl font-bold">Summer reunion</h2><p className="text-sm text-white/70">86 items · Jun 14–16</p></div></div><div className="p-5"><div className="mb-3 flex items-center justify-between text-sm"><span className="font-semibold">Review progress</span><span className="text-muted-foreground">4 of 5</span></div><div className="h-2 overflow-hidden rounded-full bg-muted"><div className="h-full w-4/5 rounded-full bg-primary" /></div><div className="mt-5 space-y-3 text-sm">{["Media organized", "Attendance confirmed", "Audiences reviewed", "Recipient preview", "Final review"].map((label, index) => <div key={label} className="flex items-center gap-3"><span className={cn("grid size-5 place-items-center rounded-full", index < 4 ? "bg-emerald-500 text-white" : "border border-border")} >{index < 4 && <Check className="size-3" />}</span><span className={cn(index === 4 && "font-semibold")}>{label}</span></div>)}</div><Button className="mt-6 w-full" onClick={() => setView("events")}>Continue review<MoveRight className="size-4" /></Button></div></Card></div>
          </div>
        </div>}
        {view === "events" && <div>
          <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between"><div><button onClick={() => setView("inbox")} className="mb-3 flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground"><ArrowLeft className="size-4" />Inbox</button><div className="flex items-center gap-2"><Badge>Draft Event</Badge><span className="text-xs text-muted-foreground">Saved just now</span></div><h1 className="mt-2 text-3xl font-bold">Summer reunion</h1><p className="mt-1 text-sm text-muted-foreground">Jun 14–16 · 86 Media items · 4 Moments</p></div><div className="flex gap-2"><Button variant="outline"><Eye className="size-4" />View as Recipient</Button><PublishDialog><Button><Upload className="size-4" />Review and publish</Button></PublishDialog></div></div>
          <div className="mb-6 overflow-x-auto scrollbar-none"><div className="flex min-w-max gap-1 rounded-xl bg-muted p-1">{(["media", "moments", "attendance", "audiences", "review"] as EditorSection[]).map((item) => <button key={item} onClick={() => setSection(item)} className={cn("rounded-lg px-4 py-2 text-sm font-semibold capitalize", section === item ? "bg-card text-foreground shadow-sm" : "text-muted-foreground")}>{item}{item === "review" && <span className="ml-2 inline-block size-2 rounded-full bg-blue-500" />}</button>)}</div></div>
          {section === "media" && <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">{photos.concat(photos.slice(0, 2)).map((photo, index) => <div key={`${photo.src}${index}`} className="group relative overflow-hidden rounded-xl"><img src={photo.src} alt={photo.alt} className="aspect-square size-full object-cover" /><button className="absolute right-2 top-2 grid size-7 place-items-center rounded-full bg-black/60 text-white opacity-0 group-hover:opacity-100"><MoreHorizontal className="size-4" /></button>{index === 2 && <Badge className="absolute bottom-2 left-2 bg-black/60 text-white">Unknown date</Badge>}</div>)}</div>}
          {section === "moments" && <div className="grid gap-6 lg:grid-cols-[380px_minmax(0,1fr)]"><div><div className="mb-4 flex items-center justify-between"><div><h2 className="font-bold">Moments</h2><p className="text-sm text-muted-foreground">Curator-only organization</p></div><Button size="sm" variant="outline"><Plus className="size-4" />New</Button></div><MomentList selected={1} /></div><Card className="overflow-hidden"><PhotoStrip className="rounded-none" /><div className="p-5"><div className="flex items-start justify-between"><div><Badge tone="blue">Jun 15</Badge><h2 className="mt-2 text-xl font-bold">Saturday picnic</h2><p className="text-sm text-muted-foreground">42 items · default capture-time order</p></div><Button variant="ghost" size="icon"><MoreHorizontal className="size-4" /></Button></div><div className="mt-5 grid gap-3 sm:grid-cols-2"><Button variant="outline"><GitMerge className="size-4" />Merge Moment</Button><Button variant="outline"><Split className="size-4" />Split or move items</Button></div><div className="mt-6 rounded-2xl bg-muted p-4"><div className="flex items-center gap-2 font-semibold"><WandSparkles className="size-4 text-blue-500" />Later arrivals follow your organization</div><p className="mt-1 text-sm text-muted-foreground">New items captured on Jun 15 will enter this merged Moment.</p></div></div></Card></div>}
          {section === "attendance" && <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_360px]"><Card className="p-5"><div className="mb-5 flex items-center justify-between"><div><h2 className="font-bold">Suggested people</h2><p className="text-sm text-muted-foreground">Confirm Attendance for Saturday picnic</p></div><Badge tone="amber">3 to review</Badge></div><div className="grid gap-3 sm:grid-cols-2">{["Maya Chen", "Leo Chen", "Nora Lee", "Eli Chen"].map((name, index) => <div className="flex items-center gap-3 rounded-xl border border-border p-3" key={name}><Avatar className="size-11 overflow-hidden rounded-full"><AvatarImage src={`https://i.pravatar.cc/96?img=${[47,7,44,5][index]}`} /><AvatarFallback>{name[0]}</AvatarFallback></Avatar><div className="flex-1"><div className="font-semibold">{name}</div><button className="text-xs text-blue-500">Inspect supporting Media</button></div><Button size="icon" variant={index < 2 ? "default" : "outline"}><Check className="size-4" /></Button></div>)}</div></Card><Card className="p-5"><h3 className="font-bold">Attendance matters</h3><p className="mt-2 text-sm leading-6 text-muted-foreground">Confirmed Attendance explains Audience proposals. Face suggestions never grant access.</p><PhotoStrip className="mt-5" /></Card></div>}
          {section === "audiences" && <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_340px]"><Card className="p-5"><AudiencePanel /></Card><Card className="h-fit p-5"><h3 className="font-bold">Privacy summary</h3><div className="mt-4 space-y-4 text-sm"><div className="flex gap-3"><span className="grid size-8 place-items-center rounded-full bg-emerald-500/15 text-emerald-500"><UserRoundCheck className="size-4" /></span><div><strong>2 recipients are present</strong><p className="text-xs text-muted-foreground">Direct Attendance is always a proposal reason.</p></div></div><div className="flex gap-3"><span className="grid size-8 place-items-center rounded-full bg-blue-500/15 text-blue-500"><UsersRound className="size-4" /></span><div><strong>2 Interest-list matches</strong><p className="text-xs text-muted-foreground">You can inspect complete Interest lists.</p></div></div><div className="flex gap-3"><span className="grid size-8 place-items-center rounded-full bg-violet-500/15 text-violet-500"><Plus className="size-4" /></span><div><strong>1 manual inclusion</strong><p className="text-xs text-muted-foreground">Priya Shah was added by you.</p></div></div></div></Card></div>}
          {section === "review" && <ReviewPage />}
        </div>}
        {view === "people" && <div><div className="mb-7 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between"><div><Badge tone="blue">People and discovery</Badge><h1 className="mt-2 text-3xl font-bold">Visibility circles</h1><p className="mt-1 text-muted-foreground">Manage discovery without changing media access.</p></div><div className="flex gap-2"><Button variant="outline"><ContactRound className="size-4" />People directory</Button><Button><Plus className="size-4" />New circle</Button></div></div><Card className="p-5"><div className="mb-5 flex items-center justify-between"><div><h2 className="font-bold">Circle membership</h2><p className="text-sm text-muted-foreground">Overlapping circles are expected. Membership is not transitive.</p></div><Button variant="outline" size="sm"><Eye className="size-4" />Preview as Maya</Button></div><div className="overflow-x-auto"><PeopleMatrix /></div><div className="mt-4 flex items-start gap-2 rounded-xl bg-blue-500/10 p-3 text-sm text-blue-600 dark:text-blue-300"><Info className="mt-0.5 size-4 shrink-0" />Changing membership may deactivate Interest-list choices. Existing Audiences and access will not change.</div></Card></div>}
        {view === "activity" && <div><h1 className="text-3xl font-bold">Curator activity</h1><p className="mt-2 text-muted-foreground">Publications, access changes, interactions, and source reconciliation.</p><Card className="mt-8 divide-y divide-border">{["You confirmed Maya and Leo in Saturday picnic", "Immich reconciliation found 18 changes", "Jordan favorited 4 Media items", "You withdrew one Media item from Lake Weekend"].map((text, index) => <div key={text} className="flex items-center gap-3 p-4"><span className="grid size-9 place-items-center rounded-full bg-muted">{index === 2 ? <MessageSquare className="size-4" /> : <Clock3 className="size-4" />}</span><div className="flex-1 text-sm font-medium">{text}</div><span className="text-xs text-muted-foreground">{index + 1}h</span></div>)}</Card></div>}
      </main>
      <nav className="fixed inset-x-3 bottom-20 z-30 flex justify-around rounded-2xl border border-border bg-card/95 p-2 shadow-xl backdrop-blur lg:hidden">{nav.slice(0, 4).map((item) => <button key={item.id} onClick={() => setView(item.id)} className={cn("flex min-w-16 flex-col items-center gap-1 rounded-xl px-2 py-1.5 text-[10px] font-semibold", view === item.id ? "bg-primary/15 text-primary" : "text-muted-foreground")}><item.icon className="size-5" />{item.label}</button>)}</nav>
    </div>
  );
}

function ReviewPage() {
  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
      <div><Card className="p-5"><div className="flex items-center justify-between"><div><Badge tone="green"><Check className="size-3" />Ready</Badge><h2 className="mt-2 text-xl font-bold">Review all Moment Audiences</h2></div><Button variant="outline" size="sm"><Eye className="size-4" />View as Recipient</Button></div><div className="mt-5"><MomentList /></div></Card></div>
      <div className="space-y-4"><Card className="p-5"><h3 className="font-bold">Publication summary</h3><dl className="mt-4 space-y-3 text-sm"><div className="flex justify-between"><dt className="text-muted-foreground">Media items</dt><dd className="font-semibold">86</dd></div><div className="flex justify-between"><dt className="text-muted-foreground">Moments</dt><dd className="font-semibold">4 reviewed</dd></div><div className="flex justify-between"><dt className="text-muted-foreground">Maximum audience</dt><dd className="font-semibold">12 recipients</dd></div><div className="flex justify-between"><dt className="text-muted-foreground">Curator-only</dt><dd className="font-semibold">1 Moment</dd></div></dl><PublishDialog><Button className="mt-6 w-full"><Upload className="size-4" />Publish Event</Button></PublishDialog></Card><Card className="p-5"><div className="flex gap-3"><CircleHelp className="size-5 text-muted-foreground" /><div><h3 className="font-semibold">Notifications come later</h3><p className="mt-1 text-sm text-muted-foreground">This prototype focuses on the publishing decision. Notification behavior is resolved separately.</p></div></div></Card></div>
    </div>
  );
}

const workflowSteps = [
  { label: "Media", detail: "86 organized", complete: true },
  { label: "Moments", detail: "4 organized", complete: true },
  { label: "Attendance", detail: "12 confirmed", complete: true },
  { label: "Audiences", detail: "4 reviewed", complete: true },
  { label: "Final review", detail: "Next step", complete: false },
];

function WorkflowProgress() {
  return (
    <div className="border-b border-border bg-card px-4 py-3">
      <div className="mb-3 flex items-center justify-between gap-4">
        <div className="flex items-center gap-2"><Badge tone="blue">Draft · 4 of 5 complete</Badge><span className="hidden text-xs text-muted-foreground sm:inline">Next: final Publication review</span></div>
        <span className="text-xs font-semibold text-primary">80%</span>
      </div>
      <div className="flex items-start">
        {workflowSteps.map((step, index) => (
          <div key={step.label} className="relative flex min-w-0 flex-1 flex-col items-center text-center">
            {index > 0 && <span className={cn("absolute right-1/2 top-2.5 h-0.5 w-full", step.complete ? "bg-primary" : "bg-border")} />}
            <span className={cn("relative z-[1] grid size-5 place-items-center rounded-full border", step.complete ? "border-primary bg-primary text-primary-foreground" : "border-primary bg-card text-primary")}>{step.complete ? <Check className="size-3" /> : <span className="size-1.5 rounded-full bg-primary" />}</span>
            <span className={cn("mt-1.5 truncate text-[10px] font-semibold sm:text-xs", !step.complete && "text-primary")}>{step.label}</span>
            <span className="hidden text-[10px] text-muted-foreground md:block">{step.detail}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function VariantB({ dark, toggle }: { dark: boolean; toggle: () => void }) {
  const [selectedMoment, setSelectedMoment] = useState(1);
  const [queueOpen, setQueueOpen] = useState(true);
  return (
    <div className="flex h-screen min-h-[680px] flex-col overflow-hidden bg-background pb-16">
      <header className="flex h-14 shrink-0 items-center gap-4 border-b border-border bg-card px-3"><Brand /><Button variant="ghost" size="icon" onClick={() => setQueueOpen((value) => !value)}><PanelLeftClose className="size-4" /></Button><div className="hidden items-center gap-2 text-sm sm:flex"><span className="text-muted-foreground">Events</span><ChevronRight className="size-3" /><strong>Summer reunion</strong><Badge>Draft</Badge></div><div className="ml-auto flex items-center gap-2"><span className="hidden text-xs text-muted-foreground md:block">Saved 10 sec ago</span><Button variant="outline" size="sm"><Eye className="size-4" />Preview</Button><PublishDialog><Button size="sm"><Upload className="size-4" />Publish</Button></PublishDialog><ThemeButton dark={dark} toggle={toggle} /></div></header>
      <div className="flex min-h-0 flex-1">
        <aside className={cn("hidden shrink-0 border-r border-border bg-card transition-all lg:block", queueOpen ? "w-80" : "w-0 overflow-hidden border-0")}>
          <div className="flex items-center justify-between border-b border-border p-3">
            <div><div className="text-xs font-bold uppercase tracking-wider text-muted-foreground">Curator work</div><div className="text-xs text-muted-foreground">2 in progress · 3 need attention</div></div>
            <Button size="icon" variant="ghost"><ListFilter className="size-4" /></Button>
          </div>
          <div className="h-[calc(100%-64px)] overflow-y-auto">
            <div className="border-b border-border p-3">
              <div className="mb-2 text-[10px] font-bold uppercase tracking-wider text-muted-foreground">Needs attention</div>
              <button className="flex w-full items-start gap-2 rounded-xl border border-red-500/25 bg-red-500/5 p-3 text-left hover:bg-red-500/10">
                <AlertTriangle className="mt-0.5 size-4 shrink-0 text-red-500" />
                <div className="min-w-0 flex-1"><div className="text-xs font-semibold">3 source files unavailable</div><div className="mt-1 text-[11px] text-muted-foreground">Lake Weekend · delivery stopped</div></div>
                <ChevronRight className="size-4 text-muted-foreground" />
              </button>
            </div>
            <div className="border-b border-border p-3">
              <div className="mb-2 flex items-center justify-between"><span className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">Work in progress</span><button className="text-[10px] font-semibold text-primary">View all</button></div>
              <button className="w-full rounded-xl border border-primary/40 bg-primary/10 p-3 text-left">
                <div className="flex items-start justify-between gap-2"><div><div className="text-xs font-bold">Summer reunion</div><div className="mt-0.5 text-[11px] text-muted-foreground">Draft Event · 86 items</div></div><Badge tone="blue">4 of 5</Badge></div>
                <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-border"><div className="h-full w-4/5 rounded-full bg-primary" /></div>
                <div className="mt-2 flex items-center justify-between text-[11px]"><span className="text-muted-foreground">Next: final review</span><span className="font-semibold text-primary">80%</span></div>
              </button>
              <button className="mt-2 w-full rounded-xl border border-border p-3 text-left hover:bg-accent">
                <div className="flex items-start justify-between gap-2"><div><div className="text-xs font-bold">Grandma's 80th</div><div className="mt-0.5 text-[11px] text-muted-foreground">Staged update · 18 changes</div></div><Badge tone="amber">3 of 5</Badge></div>
                <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-border"><div className="h-full w-3/5 rounded-full bg-amber-500" /></div>
                <div className="mt-2 text-[11px] text-muted-foreground">Next: review Audience changes</div>
              </button>
            </div>
            <div className="border-b border-border p-3">
              <button className="flex w-full items-center gap-2 rounded-lg p-2 text-left text-xs hover:bg-accent"><ImagePlus className="size-4 text-primary" /><span className="flex-1 font-semibold">2 new Source albums</span><Badge tone="blue">Triage</Badge></button>
            </div>
            <div className="p-3">
              <div className="mb-2 text-[10px] font-bold uppercase tracking-wider text-muted-foreground">Summer reunion steps</div>
              {workflowSteps.map((step, index) => <button key={step.label} className={cn("flex w-full items-center rounded-lg px-2 py-2 text-xs", index === 1 ? "bg-accent font-semibold" : "text-muted-foreground hover:bg-accent")}><span className={cn("mr-2 grid size-4 place-items-center rounded-full border", step.complete ? "border-emerald-500 bg-emerald-500 text-white" : "border-primary text-primary")}>{step.complete ? <Check className="size-2.5" /> : <span className="size-1 rounded-full bg-primary" />}</span><span className="flex-1 text-left">{step.label}</span><span className="text-[10px]">{step.detail}</span></button>)}
            </div>
          </div>
        </aside>
        <main className="min-w-0 flex-1 overflow-y-auto"><WorkflowProgress /><div className="sticky top-0 z-10 flex items-center gap-3 border-b border-border bg-background/95 px-4 py-3 backdrop-blur"><div><h1 className="text-lg font-bold">Moment organizer</h1><p className="text-xs text-muted-foreground">4 Moments · 86 items · select a Moment to inspect privacy</p></div><div className="ml-auto hidden gap-1 sm:flex"><Button size="sm" variant="outline"><GitMerge className="size-3" />Merge</Button><Button size="sm" variant="outline"><Split className="size-3" />Split</Button><Button size="sm" variant="outline"><Plus className="size-3" />Moment</Button></div></div><div className="grid grid-cols-[150px_minmax(0,1fr)] gap-1 p-3 sm:grid-cols-[190px_minmax(0,1fr)]">{moments.map((moment, row) => <div key={moment.title} className="contents"><button onClick={() => setSelectedMoment(row)} className={cn("sticky left-0 z-[1] min-h-32 rounded-l-xl border border-r-0 border-border p-3 text-left", selectedMoment === row ? "border-primary bg-primary/10" : "bg-card hover:bg-accent")}><div className="text-xs font-bold">{moment.title}</div><div className="mt-1 text-[11px] text-muted-foreground">{moment.date} · {moment.items}</div><div className="mt-3 flex flex-col items-start gap-1"><Badge tone={moment.color}>{moment.audience}</Badge>{row === 1 && <Badge tone="green">12 present</Badge>}</div></button><button onClick={() => setSelectedMoment(row)} className={cn("grid min-w-0 grid-cols-4 gap-1 overflow-hidden rounded-r-xl border border-l-0 border-border p-1", selectedMoment === row && "border-primary bg-primary/10")}>{photos.slice(row, row + 4).map((photo, index) => <div key={`${photo.src}${index}`} className="relative min-w-0 overflow-hidden rounded-lg"><img src={photo.src} alt={photo.alt} className="h-32 w-full object-cover sm:h-40" />{row === 2 && index === 3 && <div className="absolute inset-0 grid place-items-center bg-black/60 text-xs font-bold text-white">+5</div>}</div>)}</button></div>)}</div></main>
        <aside className="hidden w-96 shrink-0 overflow-y-auto border-l border-border bg-card xl:block"><div className="border-b border-border p-4"><div className="flex items-start justify-between"><div><Badge tone="blue">Jun 15</Badge><h2 className="mt-2 text-lg font-bold">{moments[selectedMoment].title}</h2><p className="text-xs text-muted-foreground">{moments[selectedMoment].items} items · {moments[selectedMoment].people}</p></div><Button size="icon" variant="ghost"><MoreHorizontal className="size-4" /></Button></div></div><div className="p-4"><AudiencePanel compact /></div></aside>
      </div>
      <div className="fixed inset-x-0 bottom-16 z-30 border-t border-border bg-card p-2 xl:hidden"><div className="flex items-center justify-between"><div><div className="text-sm font-semibold">{moments[selectedMoment].title}</div><div className="text-xs text-muted-foreground">{moments[selectedMoment].audience}</div></div><Button size="sm"><ShieldCheck className="size-4" />Inspect Audience</Button></div></div>
    </div>
  );
}

function VariantC({ dark, toggle }: { dark: boolean; toggle: () => void }) {
  const [selectedMoment, setSelectedMoment] = useState(1);
  const [privacyOpen, setPrivacyOpen] = useState(true);
  const visiblePhotos = useMemo(() => photos.concat(photos.slice(0, 4)), []);
  return (
    <div className="relative min-h-screen overflow-hidden bg-zinc-950 pb-24 text-white">
      <div className="fixed inset-x-0 top-0 z-20 flex h-16 items-center gap-3 bg-gradient-to-b from-black/80 to-transparent px-4 sm:px-6"><button className="grid size-10 place-items-center rounded-full bg-black/35 backdrop-blur"><Menu className="size-5" /></button><Brand /><Badge className="hidden bg-white/15 text-white sm:inline-flex">Draft Event</Badge><div className="ml-auto flex items-center gap-2"><button className="grid size-10 place-items-center rounded-full bg-black/35 backdrop-blur"><Search className="size-5" /></button><ThemeButton dark={dark} toggle={toggle} /><PublishDialog><Button className="bg-white text-zinc-950 hover:bg-white/90"><Upload className="size-4" />Review</Button></PublishDialog></div></div>
      <div className="relative h-[58vh] min-h-[440px] overflow-hidden"><div className="grid h-full grid-cols-3 grid-rows-2 gap-1">{visiblePhotos.slice(0, 6).map((photo, index) => <button key={`${photo.src}${index}`} className={cn("group relative overflow-hidden", index === 0 && "col-span-2 row-span-2")}><img src={photo.src} alt={photo.alt} className="size-full object-cover transition-transform duration-500 group-hover:scale-105" /><div className="absolute inset-0 bg-black/0 transition-colors group-hover:bg-black/20" /><span className="absolute right-3 top-3 grid size-7 place-items-center rounded-full border border-white/50 bg-black/20 opacity-0 group-hover:opacity-100"><Check className="size-4" /></span></button>)}</div><div className="pointer-events-none absolute inset-x-0 bottom-0 h-48 bg-gradient-to-t from-zinc-950 to-transparent" /><div className="absolute bottom-8 left-5 sm:left-8"><div className="mb-2 flex gap-2"><Badge className="bg-white/15 text-white">Summer reunion</Badge><Badge className="bg-blue-500/80 text-white">Saved</Badge></div><h1 className="text-3xl font-bold sm:text-5xl">{moments[selectedMoment].title}</h1><p className="mt-2 text-white/70">{moments[selectedMoment].date} · {moments[selectedMoment].items} Media items</p></div></div>
      <div className="relative z-10 mx-auto -mt-2 grid max-w-[1500px] gap-5 px-4 sm:px-6 xl:grid-cols-[minmax(0,1fr)_390px]">
        <div><div className="mb-3 flex items-center justify-between"><div><h2 className="font-semibold">Event timeline</h2><p className="text-xs text-white/50">Drag items between Moments on desktop. Select and move on mobile.</p></div><div className="flex gap-1"><Button size="sm" className="border-white/15 bg-white/10 text-white hover:bg-white/20"><GitMerge className="size-4" />Merge</Button><Button size="sm" className="border-white/15 bg-white/10 text-white hover:bg-white/20"><Split className="size-4" />Split</Button></div></div><div className="scrollbar-none flex snap-x gap-3 overflow-x-auto pb-4">{moments.map((moment, index) => <button key={moment.title} onClick={() => setSelectedMoment(index)} className={cn("min-w-64 snap-start rounded-2xl border p-3 text-left", selectedMoment === index ? "border-blue-400 bg-blue-500/15" : "border-white/10 bg-white/5 hover:bg-white/10")}><PhotoStrip offset={index % 3} /><div className="mt-3 flex items-start justify-between"><div><div className="font-semibold">{moment.title}</div><div className="text-xs text-white/50">{moment.date} · {moment.items} items</div></div>{selectedMoment === index && <span className="grid size-6 place-items-center rounded-full bg-blue-500"><Check className="size-3" /></span>}</div><div className="mt-3"><Badge className={cn("text-white", index === 2 ? "bg-white/10" : "bg-blue-500/30")}>{moment.audience}</Badge></div></button>)}</div><div className="mt-3 grid gap-3 sm:grid-cols-3"><button className="rounded-2xl border border-white/10 bg-white/5 p-4 text-left hover:bg-white/10"><WandSparkles className="mb-3 size-5 text-blue-300" /><div className="font-semibold">Confirm Attendance</div><div className="mt-1 text-xs text-white/50">3 suggestions to review</div></button><button onClick={() => setPrivacyOpen(true)} className="rounded-2xl border border-blue-400/30 bg-blue-500/10 p-4 text-left hover:bg-blue-500/15"><ShieldCheck className="mb-3 size-5 text-blue-300" /><div className="font-semibold">Review Audience</div><div className="mt-1 text-xs text-white/50">4 explanations available</div></button><button className="rounded-2xl border border-white/10 bg-white/5 p-4 text-left hover:bg-white/10"><Eye className="mb-3 size-5 text-blue-300" /><div className="font-semibold">Preview as Recipient</div><div className="mt-1 text-xs text-white/50">Check filtered Event views</div></button></div></div>
        <aside className={cn("rounded-3xl border border-white/10 bg-zinc-900 p-5 shadow-2xl transition-all", privacyOpen ? "block" : "hidden xl:block")}><div className="mb-5 flex items-start justify-between"><div><Badge className="bg-blue-500/20 text-blue-200"><ShieldCheck className="size-3" />Privacy lens</Badge><h2 className="mt-3 text-xl font-bold">Who can see this?</h2><p className="mt-1 text-xs text-white/50">Reasons are visible only to the Curator.</p></div><button onClick={() => setPrivacyOpen(false)} className="rounded-full p-2 hover:bg-white/10"><X className="size-4" /></button></div><div className="space-y-2">{people.map((person) => <div key={person.name} className="rounded-2xl bg-white/5 p-3"><div className="flex gap-3"><Avatar className="size-9 shrink-0 overflow-hidden rounded-full"><AvatarImage src={person.image} /><AvatarFallback>{person.initials}</AvatarFallback></Avatar><div className="min-w-0"><div className="text-sm font-semibold">{person.name}</div><div className="mt-1 flex flex-wrap gap-1">{person.reasons.map((reason) => <Badge key={reason.label} tone={reason.tone}>{reason.label}</Badge>)}</div></div></div></div>)}</div><button className="mt-4 flex w-full items-center justify-center gap-2 rounded-xl border border-white/10 p-3 text-xs font-semibold hover:bg-white/10"><Eye className="size-4" />See full Audience reasoning</button></aside>
      </div>
      {!privacyOpen && <button onClick={() => setPrivacyOpen(true)} className="fixed bottom-24 right-5 z-30 flex items-center gap-2 rounded-full bg-blue-500 px-4 py-3 text-sm font-bold shadow-xl"><ShieldCheck className="size-4" />Privacy lens</button>}
    </div>
  );
}

export default function App() {
  const { variant, setVariant } = useVariant();
  const { dark, toggle } = useTheme();
  const { accent, setAccent } = useAccent(dark);
  return (
    <>
      {variant === "A" && <VariantA dark={dark} toggle={toggle} />}
      {variant === "B" && <VariantB dark={dark} toggle={toggle} />}
      {variant === "C" && <VariantC dark={dark} toggle={toggle} />}
      <PrototypeSwitcher variant={variant} setVariant={setVariant} accent={accent} setAccent={setAccent} />
    </>
  );
}
