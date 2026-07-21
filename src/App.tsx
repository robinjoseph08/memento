// Three responsive Recipient experience variants, switchable via ?variant=, on a throwaway single-page prototype.
import { useEffect, useId, useMemo, useRef, useState } from "react";
import {
  ArrowLeft,
  ArrowRight,
  Bell,
  CalendarDays,
  Check,
  ChevronLeft,
  ChevronRight,
  Clock3,
  Download,
  Heart,
  Home,
  Images,
  Info,
  LayoutGrid,
  ListFilter,
  Mail,
  MapPin,
  Menu,
  MessageCircle,
  Moon,
  MoreHorizontal,
  Play,
  Search,
  Settings,
  ShieldCheck,
  Sparkles,
  Sun,
  UserRound,
  UsersRound,
  X,
} from "lucide-react";
import {
  Avatar,
  AvatarFallback,
  AvatarImage,
  Badge,
  Button,
  Card,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "./components/ui";
import { cn } from "./lib/utils";

type VariantKey = "A" | "B" | "C";
type View = "photos" | "events" | "people" | "favorites";
type Photo = {
  id: number;
  src: string;
  alt: string;
  date: string;
  event?: string;
  loose?: boolean;
  video?: boolean;
};

type Publication = {
  id: string;
  title: string;
  detail: string;
  published: string;
  event?: string;
  photoIds: number[];
};

const photos: Photo[] = [
  { id: 1, src: "https://images.unsplash.com/photo-1529156069898-49953e39b3ac?auto=format&fit=crop&w=1200&q=85", alt: "Family gathering outdoors", date: "June 16, 2026", event: "Summer reunion" },
  { id: 2, src: "https://images.unsplash.com/photo-1609220136736-443140cffec6?auto=format&fit=crop&w=1200&q=85", alt: "Family walking together", date: "June 16, 2026", event: "Summer reunion" },
  { id: 3, src: "https://images.unsplash.com/photo-1500530855697-b586d89ba3ee?auto=format&fit=crop&w=1200&q=85", alt: "Picnic in a green field", date: "June 15, 2026", event: "Summer reunion" },
  { id: 4, src: "https://images.unsplash.com/photo-1533488765986-dfa2a9939acd?auto=format&fit=crop&w=1200&q=85", alt: "Children playing outside", date: "June 15, 2026", event: "Summer reunion", video: true },
  { id: 5, src: "https://images.unsplash.com/photo-1499209974431-9dddcece7f88?auto=format&fit=crop&w=1200&q=85", alt: "Summer field at sunset", date: "June 15, 2026", event: "Summer reunion" },
  { id: 6, src: "https://images.unsplash.com/photo-1504151932400-72d4384f04b3?auto=format&fit=crop&w=1200&q=85", alt: "Parent and child together", date: "June 14, 2026", event: "Summer reunion" },
  { id: 7, src: "https://images.unsplash.com/photo-1474552226712-ac0f0961a954?auto=format&fit=crop&w=1200&q=85", alt: "Couple laughing outdoors", date: "May 28, 2026", event: "Grandma's 80th" },
  { id: 8, src: "https://images.unsplash.com/photo-1511895426328-dc8714191300?auto=format&fit=crop&w=1200&q=85", alt: "Family seated together", date: "May 28, 2026", event: "Grandma's 80th" },
  { id: 9, src: "https://images.unsplash.com/photo-1513159446162-54eb8bdaa79b?auto=format&fit=crop&w=1200&q=85", alt: "Birthday celebration", date: "May 28, 2026", event: "Grandma's 80th" },
  { id: 10, src: "https://images.unsplash.com/photo-1490750967868-88aa4486c946?auto=format&fit=crop&w=1200&q=85", alt: "Wildflowers in the garden", date: "May 20, 2026", loose: true },
  { id: 11, src: "https://images.unsplash.com/photo-1529333166437-7750a6dd5a70?auto=format&fit=crop&w=1200&q=85", alt: "Family enjoying a walk", date: "April 12, 2026", event: "Spring break" },
  { id: 12, src: "https://images.unsplash.com/photo-1542037104857-ffbb0b9155fb?auto=format&fit=crop&w=1200&q=85", alt: "Family smiling together", date: "April 12, 2026", event: "Spring break" },
  { id: 13, src: "https://images.unsplash.com/photo-1485217988980-11786ced9454?auto=format&fit=crop&w=1200&q=85", alt: "Family on a sunny day", date: "April 11, 2026", event: "Spring break" },
  { id: 14, src: "https://images.unsplash.com/photo-1503454537195-1dcabb73ffb9?auto=format&fit=crop&w=1200&q=85", alt: "Child smiling outdoors", date: "March 30, 2026", loose: true },
];

const publications: Publication[] = [
  { id: "reunion", title: "Summer reunion", detail: "24 new photos", published: "Published today", event: "Summer reunion", photoIds: [1, 2, 3, 4] },
  { id: "grandma", title: "Grandma's 80th", detail: "8 photos added", published: "Updated yesterday", event: "Grandma's 80th", photoIds: [7, 8, 9] },
  { id: "loose", title: "A couple from the garden", detail: "2 new photos", published: "Published Monday", photoIds: [10, 14] },
];

const people = [
  { id: 1, name: "Maya Chen", relation: "Cousin", image: "https://i.pravatar.cc/120?img=47", interested: true },
  { id: 2, name: "Leo Chen", relation: "Maya's son", image: "https://i.pravatar.cc/120?img=7", interested: true },
  { id: 3, name: "Nora Lee", relation: "Cousin", image: "https://i.pravatar.cc/120?img=44", interested: false },
  { id: 4, name: "Jordan Lee", relation: "Nora's partner", image: "https://i.pravatar.cc/120?img=12", interested: false },
  { id: 5, name: "Priya Shah", relation: "Aunt", image: "https://i.pravatar.cc/120?img=32", interested: true },
  { id: 6, name: "Eli Chen", relation: "Uncle", image: "https://i.pravatar.cc/120?img=5", interested: false },
];

const variantNames: Record<VariantKey, string> = {
  A: "Selected Photos library",
  B: "Rejected publication feed",
  C: "Events page direction",
};

const navItems: Array<{ id: View; label: string; icon: typeof Home }> = [
  { id: "photos", label: "Photos", icon: Images },
  { id: "events", label: "Events", icon: CalendarDays },
  { id: "people", label: "People", icon: UsersRound },
  { id: "favorites", label: "Favorites", icon: Heart },
];

function useVariant() {
  const read = (): VariantKey => {
    const value = new URLSearchParams(window.location.search).get("variant")?.toUpperCase();
    return value === "B" || value === "C" ? value : "A";
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
    setDark((current) => !current);
  };
  return { dark, toggle };
}

function PrototypeSwitcher({ variant, setVariant }: { variant: VariantKey; setVariant: (value: VariantKey) => void }) {
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
    <div className="fixed bottom-[5.5rem] left-1/2 z-[100] flex -translate-x-1/2 items-center rounded-full border border-white/15 bg-zinc-950 p-1.5 text-white shadow-2xl shadow-black/40 lg:bottom-5">
      <button className="rounded-full p-2 hover:bg-white/15" onClick={() => cycle(-1)} aria-label="Previous variant"><ArrowLeft className="size-4" /></button>
      <div className="min-w-52 px-3 text-center text-xs font-semibold"><span className="text-sky-300">{variant}</span> · {variantNames[variant]}</div>
      <button className="rounded-full p-2 hover:bg-white/15" onClick={() => cycle(1)} aria-label="Next variant"><ArrowRight className="size-4" /></button>
    </div>
  );
}

function MementoMark({ className }: { className?: string }) {
  const id = useId();
  return (
    <svg className={className} viewBox="160 200 704 640" role="img" aria-label="Memento">
      <defs>
        <linearGradient id={`${id}-left`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--primary)" stopOpacity=".78" /><stop offset="1" stopColor="var(--primary)" stopOpacity=".62" /></linearGradient>
        <linearGradient id={`${id}-right`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--primary)" /><stop offset="1" stopColor="var(--primary)" stopOpacity=".82" /></linearGradient>
        <linearGradient id={`${id}-hero`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--secondary-foreground)" /><stop offset="1" stopColor="var(--primary)" /></linearGradient>
      </defs>
      <rect x="246" y="270" width="410" height="500" rx="112" fill={`url(#${id}-left)`} transform="rotate(-15 451 520)" />
      <rect x="368" y="270" width="410" height="500" rx="112" fill={`url(#${id}-right)`} transform="rotate(15 573 520)" />
      <rect x="322" y="282" width="380" height="500" rx="106" fill={`url(#${id}-hero)`} />
    </svg>
  );
}

function Brand({ compact = false }: { compact?: boolean }) {
  return <div className="flex items-center gap-3"><MementoMark className="size-9 shrink-0" />{!compact && <span className="text-lg font-bold tracking-tight">Memento</span>}</div>;
}

function ThemeButton({ dark, toggle }: { dark: boolean; toggle: () => void }) {
  return <Button variant="ghost" size="icon" onClick={toggle} aria-label="Toggle color theme">{dark ? <Sun className="size-4" /> : <Moon className="size-4" />}</Button>;
}

function HeaderTools({ dark, toggle, onSettings, onOnboarding }: { dark: boolean; toggle: () => void; onSettings: () => void; onOnboarding: () => void }) {
  const [profileOpen, setProfileOpen] = useState(false);
  return (
    <div className="relative flex items-center gap-1">
      <Button variant="ghost" size="icon" aria-label="Search"><Search className="size-4" /></Button>
      <Button variant="ghost" size="icon" className="relative" aria-label="Notifications"><Bell className="size-4" /><span className="absolute right-2 top-2 size-2 rounded-full bg-primary" /></Button>
      <ThemeButton dark={dark} toggle={toggle} />
      <button onClick={() => setProfileOpen((value) => !value)} className="ml-1 rounded-full" aria-label="Open profile menu">
        <Avatar className="block size-9 overflow-hidden rounded-full"><AvatarImage src="https://i.pravatar.cc/96?img=49" /><AvatarFallback>JL</AvatarFallback></Avatar>
      </button>
      {profileOpen && <div className="absolute right-0 top-12 z-40 w-64 rounded-2xl border border-border bg-card p-2 text-card-foreground shadow-2xl">
        <div className="flex items-center gap-3 border-b border-border p-3"><Avatar className="size-10 overflow-hidden rounded-full"><AvatarImage src="https://i.pravatar.cc/96?img=49" /><AvatarFallback>JL</AvatarFallback></Avatar><div><div className="text-sm font-bold">Jamie Lee</div><div className="text-xs text-muted-foreground">jamie@example.com</div></div></div>
        <button onClick={() => { onSettings(); setProfileOpen(false); }} className="mt-1 flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-sm font-semibold hover:bg-accent"><Settings className="size-4" />Settings</button>
        <button onClick={() => { onOnboarding(); setProfileOpen(false); }} className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-sm font-semibold hover:bg-accent"><Sparkles className="size-4" />Replay onboarding</button>
      </div>}
    </div>
  );
}

function BottomNav({ view, setView, eventFirst = false }: { view: View; setView: (view: View) => void; eventFirst?: boolean }) {
  const items = eventFirst ? [navItems[1], navItems[0], navItems[2], navItems[3]] : navItems;
  return (
    <nav className="fixed inset-x-2 bottom-2 z-40 flex justify-around rounded-2xl border border-border bg-card/95 p-1.5 shadow-2xl backdrop-blur lg:hidden">
      {items.map((item) => <button key={item.id} onClick={() => setView(item.id)} className={cn("flex min-w-16 flex-col items-center gap-1 rounded-xl px-2 py-1.5 text-[10px] font-semibold", view === item.id ? "bg-primary/15 text-primary" : "text-muted-foreground")}><item.icon className="size-5" />{item.label}</button>)}
    </nav>
  );
}

function SettingsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [frequency, setFrequency] = useState("Immediately");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogTitle className="text-2xl font-bold">Settings</DialogTitle>
        <DialogDescription className="mt-1">Manage notifications and this browser session.</DialogDescription>
        <section className="mt-6">
          <div className="flex items-center gap-3"><span className="grid size-10 place-items-center rounded-full bg-primary/15 text-primary"><Mail className="size-5" /></span><div><h3 className="font-bold">New publication emails</h3><p className="text-sm text-muted-foreground">Choose when Memento tells you about newly shared material.</p></div></div>
          <div className="mt-4 grid gap-2 sm:grid-cols-3">{["Immediately", "Weekly", "None"].map((option) => <button key={option} onClick={() => setFrequency(option)} className={cn("rounded-2xl border p-4 text-left", frequency === option ? "border-primary bg-primary/10" : "border-border hover:bg-accent")}><div className="flex items-center justify-between"><span className="font-semibold">{option}</span>{frequency === option && <Check className="size-4 text-primary" />}</div><div className="mt-1 text-xs text-muted-foreground">{option === "Immediately" ? "After each publication" : option === "Weekly" ? "One Sunday summary" : "No publication email"}</div></button>)}</div>
        </section>
        <section className="mt-7 border-t border-border pt-6"><div className="flex items-start gap-3"><ShieldCheck className="mt-0.5 size-5 text-emerald-500" /><div className="flex-1"><h3 className="font-bold">This browser is trusted</h3><p className="mt-1 text-sm text-muted-foreground">Last active today. It stays signed in for up to one year while you continue using it.</p><button className="mt-3 text-sm font-semibold text-red-500">Sign out this browser</button></div></div></section>
      </DialogContent>
    </Dialog>
  );
}

function OnboardingDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [step, setStep] = useState(1);
  const steps = ["Invitation", "Privacy", "Interests", "Notifications", "Ready"];
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <div className="mb-6 flex gap-1">{steps.map((label, index) => <div key={label} className="min-w-0 flex-1"><div className={cn("h-1.5 rounded-full", index + 1 <= step ? "bg-primary" : "bg-muted")} /><div className="mt-1 hidden text-center text-[10px] text-muted-foreground sm:block">{label}</div></div>)}</div>
        {step === 1 && <div><Badge tone="blue">Invitation from Robin</Badge><DialogTitle className="mt-4 text-3xl font-bold">Welcome to Memento</DialogTitle><DialogDescription className="mt-3 text-base leading-7">Robin invited you to privately view family photos and videos selected for you.</DialogDescription><Card className="mt-6 overflow-hidden"><div className="grid grid-cols-3 gap-1">{photos.slice(0, 3).map((photo) => <img key={photo.id} src={photo.src} alt="" className="aspect-square size-full object-cover" />)}</div></Card></div>}
        {step === 2 && <div><span className="grid size-12 place-items-center rounded-full bg-emerald-500/15 text-emerald-500"><ShieldCheck className="size-6" /></span><DialogTitle className="mt-4 text-3xl font-bold">Private by design</DialogTitle><DialogDescription className="mt-3 text-base leading-7">You only see photos Robin has approved for you. There are no public links, and Memento never reveals photos you cannot access.</DialogDescription><div className="mt-6 space-y-3 text-sm"><div className="flex gap-3 rounded-xl bg-muted p-4"><Check className="size-5 text-emerald-500" />Comments stay with people who can view that photo.</div><div className="flex gap-3 rounded-xl bg-muted p-4"><Check className="size-5 text-emerald-500" />Favorites aren't shared with other recipients.</div></div></div>}
        {step === 3 && <div><DialogTitle className="text-3xl font-bold">Whose photos would you like to see?</DialogTitle><DialogDescription className="mt-2 leading-6">Choose family members you care about. When they attend an Event, Robin can use your choices to share those photos with you even if you weren't there.</DialogDescription><div className="mt-5 grid gap-2 sm:grid-cols-2">{people.slice(0, 4).map((person) => <button key={person.id} className={cn("flex items-center gap-3 rounded-xl border p-3 text-left", person.interested && "border-primary bg-primary/10")}><Avatar className="size-10 overflow-hidden rounded-full"><AvatarImage src={person.image} /><AvatarFallback>{person.name[0]}</AvatarFallback></Avatar><div className="min-w-0 flex-1"><div className="truncate text-sm font-semibold">{person.name}</div><div className="text-xs text-muted-foreground">{person.relation}</div></div>{person.interested && <Check className="size-4 text-primary" />}</button>)}</div></div>}
        {step === 4 && <div><DialogTitle className="text-3xl font-bold">How should we let you know?</DialogTitle><DialogDescription className="mt-2">You can change this any time in Settings.</DialogDescription><div className="mt-6 space-y-2">{["Email me immediately", "Send a weekly summary", "Don't email me"].map((option, index) => <button key={option} className={cn("flex w-full items-center gap-3 rounded-2xl border p-4 text-left", index === 0 ? "border-primary bg-primary/10" : "border-border")}><span className={cn("grid size-5 place-items-center rounded-full border", index === 0 ? "border-primary bg-primary" : "border-border")}>{index === 0 && <Check className="size-3 text-primary-foreground" />}</span><span className="font-semibold">{option}</span></button>)}</div></div>}
        {step === 5 && <div className="py-4 text-center"><span className="mx-auto grid size-16 place-items-center rounded-full bg-primary/15 text-primary"><Sparkles className="size-8" /></span><DialogTitle className="mt-5 text-3xl font-bold">You're ready</DialogTitle><DialogDescription className="mx-auto mt-3 max-w-md text-base leading-7">Your private family collection is waiting. Start with the newest photos shared with you.</DialogDescription></div>}
        <div className="mt-8 flex items-center justify-between"><Button variant="ghost" onClick={() => setStep((current) => Math.max(1, current - 1))} disabled={step === 1}><ChevronLeft className="size-4" />Back</Button>{step < 5 ? <Button onClick={() => setStep((current) => current + 1)}>{step === 1 ? "Accept invitation" : "Continue"}<ChevronRight className="size-4" /></Button> : <Button onClick={() => onOpenChange(false)}>View my photos</Button>}</div>
      </DialogContent>
    </Dialog>
  );
}

function MediaViewer({ photo, open, onOpenChange }: { photo: Photo | null; open: boolean; onOpenChange: (open: boolean) => void }) {
  const [favorite, setFavorite] = useState(false);
  if (!photo) return null;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="h-[calc(100vh-1rem)] w-[calc(100vw-1rem)] max-h-none max-w-none overflow-hidden rounded-2xl p-0 sm:h-[calc(100vh-3rem)] sm:w-[calc(100vw-3rem)]">
        <DialogTitle className="sr-only">{photo.alt}</DialogTitle>
        <div className="grid h-full min-h-0 lg:grid-cols-[minmax(0,1fr)_380px]">
          <div className="relative grid h-[52vh] min-h-0 place-items-center overflow-hidden bg-zinc-950 lg:h-full"><img src={photo.src} alt={photo.alt} className="max-h-full max-w-full object-contain" />{photo.video && <span className="absolute grid size-16 place-items-center rounded-full bg-white/90 text-zinc-950 shadow-xl"><Play className="ml-1 size-7 fill-current" /></span>}<div className="absolute left-3 top-3 flex gap-2 lg:hidden"><button className="grid size-10 place-items-center rounded-full bg-black/55 text-white" onClick={() => setFavorite((value) => !value)}><Heart className={cn("size-5", favorite && "fill-red-500 text-red-500")} /></button><button className="grid size-10 place-items-center rounded-full bg-black/55 text-white"><Download className="size-5" /></button></div></div>
          <aside className="overflow-y-auto bg-card p-5"><div className="hidden items-center gap-2 lg:flex"><Button variant={favorite ? "secondary" : "outline"} size="sm" onClick={() => setFavorite((value) => !value)}><Heart className={cn("size-4", favorite && "fill-current")} />{favorite ? "Favorited" : "Favorite"}</Button><Button variant="outline" size="sm"><Download className="size-4" />Original</Button><Button variant="ghost" size="icon"><MoreHorizontal className="size-4" /></Button></div>{favorite && <div className="mt-4 rounded-xl bg-primary/10 p-3 text-xs text-primary">Favorites aren't shared with other recipients.</div>}<div className="mt-5"><h3 className="font-bold">{photo.event ?? "Shared photo"}</h3><p className="mt-1 text-sm text-muted-foreground">{photo.date}{photo.loose ? " · Shared independently" : ""}</p></div><div className="mt-6 border-t border-border pt-5"><div className="flex items-center justify-between"><h3 className="font-bold">Comments</h3><Badge>2</Badge></div><div className="mt-4 space-y-4"><div className="flex gap-3"><Avatar className="size-8 shrink-0 overflow-hidden rounded-full"><AvatarImage src="https://i.pravatar.cc/96?img=47" /><AvatarFallback>MC</AvatarFallback></Avatar><div><div className="text-xs font-bold">Maya Chen <span className="font-normal text-muted-foreground">2h</span></div><p className="mt-1 text-sm">This one made me laugh so much!</p></div></div><div className="flex gap-3"><Avatar className="size-8 shrink-0 overflow-hidden rounded-full"><AvatarImage src="https://i.pravatar.cc/96?img=11" /><AvatarFallback>RJ</AvatarFallback></Avatar><div><div className="text-xs font-bold">Robin <span className="font-normal text-muted-foreground">1h</span></div><p className="mt-1 text-sm">I almost missed that moment.</p></div></div></div><div className="mt-5 flex gap-2"><input className="min-w-0 flex-1 rounded-full border border-border bg-background px-4 text-sm outline-none focus:border-primary" placeholder="Write a comment" /><Button size="sm">Send</Button></div><p className="mt-3 text-[11px] leading-4 text-muted-foreground">Comments are visible to Robin and recipients who can access this photo.</p></div></aside>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function PhotoTile({ photo, onOpen, className }: { photo: Photo; onOpen: (photo: Photo) => void; className?: string }) {
  return <button onClick={() => onOpen(photo)} className={cn("group relative overflow-hidden rounded-lg bg-muted text-left", className)}><img src={photo.src} alt={photo.alt} className="size-full object-cover transition duration-300 group-hover:scale-[1.03]" />{photo.video && <span className="absolute left-2 top-2 grid size-7 place-items-center rounded-full bg-black/55 text-white"><Play className="ml-0.5 size-3.5 fill-current" /></span>}{photo.loose && <Badge className="absolute bottom-2 left-2 bg-black/55 text-white">Shared photo</Badge>}<span className="absolute inset-0 bg-black/0 transition group-hover:bg-black/10" /></button>;
}

function NewForYou({ seen, onSeen, onOpen, style = "rail" }: { seen: Set<string>; onSeen: (id: string) => void; onOpen: (photo: Photo) => void; style?: "rail" | "feed" | "compact" }) {
  const unseen = publications.filter((publication) => !seen.has(publication.id));
  if (unseen.length === 0) return <div className="rounded-2xl border border-border bg-card p-5"><div className="flex items-center gap-3"><span className="grid size-10 place-items-center rounded-full bg-emerald-500/15 text-emerald-500"><Check className="size-5" /></span><div><h3 className="font-bold">You're all caught up</h3><p className="text-sm text-muted-foreground">New Publications will appear here.</p></div></div></div>;
  if (style === "feed") return <div className="space-y-6">{unseen.map((publication) => { const publicationPhotos = publication.photoIds.map((id) => photos.find((photo) => photo.id === id)!).filter(Boolean); return <article key={publication.id} className="overflow-hidden rounded-3xl border border-border bg-card shadow-sm"><div className="flex items-start justify-between p-5 sm:p-6"><div><Badge tone="blue"><Sparkles className="size-3" />New for you</Badge><h2 className="mt-3 text-2xl font-bold">{publication.title}</h2><p className="mt-1 text-sm text-muted-foreground">{publication.detail} · {publication.published}</p></div><button onClick={() => onSeen(publication.id)} className="text-xs font-semibold text-muted-foreground hover:text-foreground">Mark seen</button></div><div className={cn("grid gap-1", publicationPhotos.length === 2 ? "grid-cols-2" : "grid-cols-3", publicationPhotos.length > 3 && "grid-rows-2")}><PhotoTile photo={publicationPhotos[0]} onOpen={() => { onSeen(publication.id); onOpen(publicationPhotos[0]); }} className={cn("aspect-square rounded-none", publicationPhotos.length > 3 && "col-span-2 row-span-2")} />{publicationPhotos.slice(1).map((photo) => <PhotoTile key={photo.id} photo={photo} onOpen={() => { onSeen(publication.id); onOpen(photo); }} className="aspect-square rounded-none" />)}</div><div className="flex items-center justify-between p-4 sm:px-6"><Button variant="ghost" size="sm" onClick={() => onSeen(publication.id)}>View {publication.event ? "Event" : "photos"}<ChevronRight className="size-4" /></Button><div className="flex gap-1"><Button variant="ghost" size="icon"><Heart className="size-4" /></Button><Button variant="ghost" size="icon"><MessageCircle className="size-4" /></Button></div></div></article>; })}</div>;
  return <div className={cn(style === "rail" ? "scrollbar-none flex snap-x gap-3 overflow-x-auto pb-2" : "space-y-3")}>{unseen.map((publication) => { const publicationPhotos = publication.photoIds.map((id) => photos.find((photo) => photo.id === id)!).filter(Boolean); return <button key={publication.id} onClick={() => { onSeen(publication.id); onOpen(publicationPhotos[0]); }} className={cn("group overflow-hidden rounded-2xl border border-border bg-card text-left shadow-sm", style === "rail" ? "w-[82vw] shrink-0 snap-start sm:max-w-sm lg:max-w-none lg:flex-1" : "flex w-full p-2")}><div className={cn("grid grid-cols-3 gap-0.5 overflow-hidden", style === "rail" ? "h-40" : "h-24 w-36 shrink-0 rounded-xl")}>{publicationPhotos.slice(0, 3).map((photo) => <img key={photo.id} src={photo.src} alt="" className="size-full object-cover transition group-hover:scale-[1.02]" />)}</div><div className={cn("p-4", style === "compact" && "min-w-0 py-2")}><div className="flex items-center gap-2"><span className="size-2 rounded-full bg-primary" /><span className="text-xs font-bold uppercase tracking-wide text-primary">New for you</span></div><h3 className="mt-1 truncate font-bold">{publication.title}</h3><p className="mt-1 text-sm text-muted-foreground">{publication.detail} · {publication.published}</p></div></button>; })}</div>;
}

const galleryRatios = [1.5, 1.15, 0.82, 1.78, 1.32, 0.9, 1.6, 1.05, 1.4, 0.78, 1.72, 1.2];

function DenseGallery({ items, onOpen, compact = false }: { items: Photo[]; onOpen: (photo: Photo) => void; compact?: boolean }) {
  return <div className="flex flex-wrap gap-1">{items.map((photo, index) => { const ratio = galleryRatios[index % galleryRatios.length]; return <button key={`${photo.id}-${index}`} onClick={() => onOpen(photo)} style={{ flexBasis: `${ratio * (compact ? 7 : 9)}rem`, flexGrow: ratio }} className={cn("group relative h-24 min-w-20 overflow-hidden bg-muted sm:h-28", compact ? "lg:h-32" : "lg:h-40 2xl:h-44")}><img src={photo.src} alt={photo.alt} className="size-full object-cover transition duration-300 group-hover:scale-[1.02]" />{photo.video && <span className="absolute left-2 top-2 grid size-7 place-items-center rounded-full bg-black/55 text-white"><Play className="ml-0.5 size-3.5 fill-current" /></span>}<span className="absolute right-2 top-2 grid size-6 place-items-center rounded-full border border-white/70 bg-black/25 text-white opacity-0 transition group-hover:opacity-100"><Check className="size-3.5" /></span></button>; })}<span className="h-0 flex-[999_1_12rem]" /></div>;
}

const timelineGroups = [
  { id: "timeline-june-16-2026", date: "Tue, June 16", railLabel: "Jun 2026", year: "2026", indexes: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11] },
  { id: "timeline-june-15-2026", date: "Mon, June 15", railLabel: "Jun 15, 2026", indexes: [2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 0] },
  { id: "timeline-may-2026", date: "Thu, May 28", railLabel: "May 2026", indexes: [6, 7, 8, 9, 10, 11, 12, 13, 0, 1, 2, 3] },
  { id: "timeline-april-2026", date: "April 11–12", railLabel: "Apr 2026", indexes: [10, 11, 12, 13, 0, 1, 2, 3, 4, 5, 6, 7] },
  { id: "timeline-2025", date: "September 2025", railLabel: "Sep 2025", year: "2025", indexes: [8, 3, 11, 5, 1, 12, 6, 9, 4, 0, 7, 2] },
  { id: "timeline-2024", date: "December 2024", railLabel: "Dec 2024", year: "2024", indexes: [4, 6, 1, 10, 13, 7, 3, 11, 0, 5, 8, 2] },
  { id: "timeline-2023", date: "July 2023", railLabel: "Jul 2023", year: "2023", indexes: [9, 2, 12, 8, 4, 10, 6, 1, 13, 3, 7, 0] },
  { id: "timeline-2022", date: "November 2022", railLabel: "Nov 2022", year: "2022", indexes: [11, 5, 0, 7, 2, 13, 9, 4, 1, 12, 6, 3] },
  { id: "timeline-2021", date: "August 2021", railLabel: "Aug 2021", year: "2021", indexes: [3, 8, 12, 1, 6, 10, 4, 13, 0, 9, 5, 2] },
];

function Timeline({ onOpen, compact = false }: { onOpen: (photo: Photo) => void; compact?: boolean }) {
  const groups = compact ? timelineGroups.slice(0, 3) : timelineGroups;
  return <div className="space-y-7">{groups.map((photoGroup) => <section id={photoGroup.id} data-timeline-date={photoGroup.railLabel} key={photoGroup.id} className="scroll-mt-24"><h3 className="mb-2 text-xs font-bold sm:text-sm">{photoGroup.date}</h3><DenseGallery items={photoGroup.indexes.map((index) => photos[index % photos.length])} onOpen={onOpen} compact={compact} /></section>)}</div>;
}

function DateRail() {
  const railRef = useRef<HTMLDivElement>(null);
  const scrubbing = useRef(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);

  useEffect(() => {
    const update = () => {
      let current = 0;
      timelineGroups.forEach((group, index) => {
        const element = document.getElementById(group.id);
        if (element && element.getBoundingClientRect().top <= 180) current = index;
      });
      setActiveIndex(current);
    };
    update();
    window.addEventListener("scroll", update, { passive: true });
    return () => window.removeEventListener("scroll", update);
  }, []);

  const indexFromY = (clientY: number) => {
    const bounds = railRef.current!.getBoundingClientRect();
    const progress = Math.max(0, Math.min(1, (clientY - bounds.top) / bounds.height));
    return Math.round(progress * (timelineGroups.length - 1));
  };
  const jumpTo = (index: number, smooth: boolean) => {
    setActiveIndex(index);
    document.getElementById(timelineGroups[index].id)?.scrollIntoView({ behavior: smooth ? "smooth" : "auto", block: "start" });
  };
  const position = (index: number) => 4 + (index / (timelineGroups.length - 1)) * 92;
  const displayIndex = hoverIndex ?? activeIndex;

  return <aside className="fixed bottom-7 right-1 top-24 z-30 hidden w-28 select-none xl:block"><div ref={railRef} className="relative h-full touch-none cursor-ns-resize" onPointerDown={(event) => { scrubbing.current = true; event.currentTarget.setPointerCapture(event.pointerId); const index = indexFromY(event.clientY); setHoverIndex(index); jumpTo(index, true); }} onPointerMove={(event) => { const index = indexFromY(event.clientY); setHoverIndex(index); if (scrubbing.current) jumpTo(index, false); }} onPointerUp={(event) => { scrubbing.current = false; event.currentTarget.releasePointerCapture(event.pointerId); }} onPointerLeave={() => { if (!scrubbing.current) setHoverIndex(null); }}><span className="absolute bottom-[4%] right-2 top-[4%] w-px bg-border" />{timelineGroups.map((group, index) => <span key={group.id} style={{ top: `${position(index)}%` }} className="absolute right-0 flex -translate-y-1/2 items-center gap-2"><span className={cn("text-[11px] font-semibold", index === activeIndex ? "text-primary" : "text-muted-foreground")}>{group.year ?? ""}</span><span className={cn("relative z-[1] rounded-full bg-background", index === activeIndex ? "size-2.5 border-2 border-primary" : "mr-0.5 size-1.5 bg-muted-foreground")} /></span>)}<span style={{ top: `${position(displayIndex)}%` }} className="pointer-events-none absolute right-6 -translate-y-1/2 whitespace-nowrap rounded-md border-b-2 border-primary bg-background/95 px-2 py-1 text-xs font-semibold shadow-lg">{timelineGroups[displayIndex].railLabel}</span></div></aside>;
}

function EventsView({ onOpen }: { onOpen: (photo: Photo) => void }) {
  const [selected, setSelected] = useState<string | null>(() => new URLSearchParams(window.location.search).get("event"));
  const eventCards = [
    { title: "Summer reunion", date: "June 14–16", count: 124, cover: photos[0], photos: photos.slice(0, 6) },
    { title: "Grandma's 80th", date: "May 28", count: 108, cover: photos[7], photos: photos.slice(6, 9) },
    { title: "Spring break", date: "April 11–12", count: 142, cover: photos[11], photos: photos.slice(10, 13) },
  ];
  const event = eventCards.find((item) => item.title === selected);
  if (event) return <div><button onClick={() => setSelected(null)} className="mb-5 flex items-center gap-2 text-sm font-semibold text-muted-foreground hover:text-foreground"><ChevronLeft className="size-4" />All Events</button><div className="relative overflow-hidden rounded-3xl"><img src={event.cover.src} alt="" className="h-64 w-full object-cover sm:h-80" /><div className="absolute inset-0 bg-gradient-to-t from-black/85 via-black/10 to-transparent" /><div className="absolute inset-x-0 bottom-0 p-6 text-white sm:p-8"><h1 className="text-3xl font-bold sm:text-4xl">{event.title}</h1><p className="mt-2 text-white/75">{event.date} · {event.count} photos and videos</p></div></div><div className="mt-6 flex items-center justify-between"><div><h2 className="font-bold">The Event</h2><p className="text-sm text-muted-foreground">Everything shared with you, in Robin's chosen order.</p></div><Button variant="outline" size="sm"><Download className="size-4" />Download Event</Button></div><div className="mt-4"><DenseGallery items={Array.from({ length: Math.min(event.count, 36) }, (_, index) => event.photos[index % event.photos.length])} onOpen={onOpen} compact /></div></div>;
  return <div><div className="mb-7"><h1 className="text-3xl font-bold">Events</h1><p className="mt-1 text-muted-foreground">Family stories collected and arranged by Robin.</p></div><div className="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">{eventCards.map((item) => <button key={item.title} onClick={() => setSelected(item.title)} className="group overflow-hidden rounded-3xl border border-border bg-card text-left shadow-sm"><div className="relative overflow-hidden"><img src={item.cover.src} alt="" className="aspect-[4/3] w-full object-cover transition duration-500 group-hover:scale-105" /><div className="absolute inset-0 bg-gradient-to-t from-black/75 via-transparent to-transparent" /><div className="absolute bottom-4 left-4 text-white"><h2 className="text-xl font-bold">{item.title}</h2><p className="text-sm text-white/75">{item.date}</p></div></div><div className="flex items-center justify-between p-4"><span className="text-sm text-muted-foreground">{item.count} photos and videos</span><ChevronRight className="size-4" /></div></button>)}</div></div>;
}

function PeopleView() {
  const [query, setQuery] = useState("");
  const [choices, setChoices] = useState(() => new Set(people.filter((person) => person.interested).map((person) => person.id)));
  const [selected, setSelected] = useState(people[0]);
  const visible = people.filter((person) => person.name.toLowerCase().includes(query.toLowerCase()));
  const toggle = (id: number) => setChoices((current) => { const next = new Set(current); next.has(id) ? next.delete(id) : next.add(id); return next; });
  return <div><div className="mb-6"><Badge tone="blue">People you can discover</Badge><h1 className="mt-3 text-3xl font-bold">People I'm interested in</h1><p className="mt-2 max-w-2xl text-muted-foreground">Choose family members you care about. When they attend an Event, Robin can use your choices to share those photos with you even if you weren't there.</p></div><div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_340px]"><Card className="overflow-hidden"><div className="border-b border-border p-4"><label className="flex items-center gap-3 rounded-full bg-muted px-4 py-2.5"><Search className="size-4 text-muted-foreground" /><input value={query} onChange={(event) => setQuery(event.target.value)} className="min-w-0 flex-1 bg-transparent text-sm outline-none" placeholder="Search People" /></label></div><div className="divide-y divide-border">{visible.map((person) => <div key={person.id} className={cn("flex items-center gap-3 p-4", selected.id === person.id && "bg-primary/5")}><button onClick={() => setSelected(person)} className="flex min-w-0 flex-1 items-center gap-3 text-left"><Avatar className="size-12 shrink-0 overflow-hidden rounded-full"><AvatarImage src={person.image} /><AvatarFallback>{person.name[0]}</AvatarFallback></Avatar><div className="min-w-0"><div className="font-bold">{person.name}</div><div className="text-sm text-muted-foreground">{person.relation}</div></div></button><button onClick={() => toggle(person.id)} className={cn("relative h-7 w-12 shrink-0 rounded-full transition", choices.has(person.id) ? "bg-primary" : "bg-muted")} aria-label={`${choices.has(person.id) ? "Remove" : "Add"} ${person.name}`}><span className={cn("absolute top-1 size-5 rounded-full bg-white transition-all", choices.has(person.id) ? "left-6" : "left-1")} /></button></div>)}</div></Card><Card className="hidden h-fit overflow-hidden lg:block"><img src={selected.image} alt="" className="aspect-[16/10] w-full object-cover" /><div className="p-5"><h2 className="text-xl font-bold">{selected.name}</h2><p className="text-sm text-muted-foreground">{selected.relation}</p><div className="mt-5 rounded-xl bg-muted p-4 text-sm leading-6"><strong>Why can I see {selected.name.split(" ")[0]}?</strong><p className="mt-1 text-muted-foreground">You and {selected.name.split(" ")[0]} are in a shared family Visibility circle managed by Robin.</p></div></div></Card></div></div>;
}

function FavoritesView({ onOpen }: { onOpen: (photo: Photo) => void }) {
  const favorites = [photos[1], photos[3], photos[7], photos[10], photos[13]];
  return <div><div className="mb-7"><span className="grid size-12 place-items-center rounded-full bg-red-500/15 text-red-500"><Heart className="size-6 fill-current" /></span><h1 className="mt-4 text-3xl font-bold">Favorites</h1><p className="mt-1 text-muted-foreground">Favorites aren't shared with other recipients.</p></div><DenseGallery items={favorites} onOpen={onOpen} compact /></div>;
}

function CommonView({ view, onOpen }: { view: View; onOpen: (photo: Photo) => void }) {
  if (view === "events") return <EventsView onOpen={onOpen} />;
  if (view === "people") return <PeopleView />;
  if (view === "favorites") return <FavoritesView onOpen={onOpen} />;
  return null;
}

type VariantProps = {
  dark: boolean;
  toggle: () => void;
  onSettings: () => void;
  onOnboarding: () => void;
  onOpen: (photo: Photo) => void;
  seen: Set<string>;
  onSeen: (id: string) => void;
};

function VariantA({ dark, toggle, onSettings, onOnboarding, onOpen, seen, onSeen }: VariantProps) {
  const [view, setView] = useState<View>(() => { const value = new URLSearchParams(window.location.search).get("view"); return value === "events" || value === "people" || value === "favorites" ? value : "photos"; });
  return <div className="min-h-screen bg-background pb-24 lg:pb-8"><aside className="fixed inset-y-0 left-0 z-20 hidden w-64 border-r border-border bg-card p-5 lg:block"><Brand /><nav className="mt-10 space-y-1">{navItems.map((item) => <button key={item.id} onClick={() => setView(item.id)} className={cn("flex w-full items-center gap-3 rounded-xl px-3 py-3 text-sm font-semibold", view === item.id ? "bg-primary/15 text-primary" : "text-muted-foreground hover:bg-accent hover:text-foreground")}><item.icon className="size-5" />{item.label}</button>)}</nav><div className="absolute inset-x-5 bottom-5 border-t border-border pt-4 text-xs text-muted-foreground"><div className="font-semibold text-foreground">Memento v0.1.0</div><div className="mt-1">1,284 photos · 76 videos</div><div className="mt-0.5">Across 6 years</div></div></aside><header className="sticky top-0 z-20 flex h-16 items-center border-b border-border bg-background/90 px-4 backdrop-blur-xl lg:ml-64 lg:px-7"><div className="lg:hidden"><Brand /></div><label className="mx-auto hidden w-full max-w-xl items-center gap-3 rounded-full bg-muted px-4 py-2.5 lg:flex"><Search className="size-4 text-muted-foreground" /><input className="min-w-0 flex-1 bg-transparent text-sm outline-none" placeholder="Search Events, dates, places, and People" /></label><HeaderTools dark={dark} toggle={toggle} onSettings={onSettings} onOnboarding={onOnboarding} /></header><main className="w-full px-4 py-7 lg:ml-64 lg:w-[calc(100%-16rem)] lg:px-8 xl:pr-24">{view === "photos" ? <><div className="mb-6 flex items-end justify-between"><div><h1 className="text-3xl font-bold">Photos</h1><p className="mt-1 text-muted-foreground">Your private family timeline</p></div><div className="hidden gap-2 sm:flex"><Button variant="outline" size="sm"><Check className="size-4" />Select photos</Button><Button variant="outline" size="sm"><ListFilter className="size-4" />Filter</Button></div></div><section className="mb-10"><div className="mb-4 flex items-center justify-between"><div><div className="flex items-center gap-2"><Sparkles className="size-4 text-primary" /><h2 className="font-bold">New for you</h2></div><p className="mt-1 text-sm text-muted-foreground">Recent Publications you haven't opened yet</p></div>{publications.some((item) => !seen.has(item.id)) && <button onClick={() => publications.forEach((item) => onSeen(item.id))} className="text-xs font-semibold text-primary">Mark all seen</button>}</div><NewForYou seen={seen} onSeen={onSeen} onOpen={onOpen} /></section><section><h2 className="mb-4 text-lg font-bold">Your timeline</h2><Timeline onOpen={onOpen} /></section></> : <CommonView view={view} onOpen={onOpen} />}</main>{view === "photos" && <DateRail />}<BottomNav view={view} setView={setView} /></div>;
}

function VariantB({ dark, toggle, onSettings, onOnboarding, onOpen, seen, onSeen }: VariantProps) {
  const [view, setView] = useState<View>("photos");
  return <div className="min-h-screen bg-background pb-24"><header className="sticky top-0 z-30 border-b border-border bg-card/95 backdrop-blur-xl"><div className="mx-auto flex h-16 max-w-7xl items-center gap-6 px-4"><Brand /><nav className="hidden h-full items-center gap-1 lg:flex">{navItems.map((item) => <button key={item.id} onClick={() => setView(item.id)} className={cn("h-full border-b-2 px-4 text-sm font-semibold", view === item.id ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground")}>{item.label}</button>)}</nav><div className="ml-auto"><HeaderTools dark={dark} toggle={toggle} onSettings={onSettings} onOnboarding={onOnboarding} /></div></div></header><main className={cn("mx-auto px-4 py-7", view === "photos" ? "max-w-6xl" : "max-w-7xl")}>{view === "photos" ? <div className="grid gap-8 lg:grid-cols-[minmax(0,720px)_280px]"><div><div className="mb-7"><Badge tone="blue"><Sparkles className="size-3" />{publications.filter((item) => !seen.has(item.id)).length} updates waiting</Badge><h1 className="mt-3 text-3xl font-bold sm:text-4xl">New from your family</h1><p className="mt-2 text-muted-foreground">Every update is selected and shared by Robin.</p></div><NewForYou seen={seen} onSeen={onSeen} onOpen={onOpen} style="feed" /><div className="my-10 flex items-center gap-4"><div className="h-px flex-1 bg-border" /><span className="text-xs font-bold uppercase tracking-wider text-muted-foreground">Earlier photos</span><div className="h-px flex-1 bg-border" /></div><Timeline onOpen={onOpen} compact /></div><aside className="hidden lg:block"><div className="sticky top-24 space-y-4"><Card className="p-5"><h3 className="font-bold">Your collection</h3><div className="mt-4 space-y-3 text-sm"><button className="flex w-full items-center justify-between"><span className="text-muted-foreground">This week</span><strong>34 photos</strong></button><button className="flex w-full items-center justify-between"><span className="text-muted-foreground">May</span><strong>46 photos</strong></button><button className="flex w-full items-center justify-between"><span className="text-muted-foreground">April</span><strong>42 photos</strong></button><button className="flex w-full items-center justify-between"><span className="text-muted-foreground">2025</span><strong>312 photos</strong></button></div></Card><Card className="p-5"><div className="flex gap-3"><Info className="mt-0.5 size-5 text-primary" /><div><h3 className="font-bold">Only what is shared with you</h3><p className="mt-2 text-sm leading-6 text-muted-foreground">Counts and previews never include private material or photos shared with someone else.</p></div></div></Card></div></aside></div> : <CommonView view={view} onOpen={onOpen} />}</main><BottomNav view={view} setView={setView} /></div>;
}

function VariantC({ dark, toggle, onSettings, onOnboarding, onOpen, seen, onSeen }: VariantProps) {
  const [view, setView] = useState<View>("events");
  const recent = publications.filter((item) => !seen.has(item.id));
  return <div className="min-h-screen bg-background pb-24"><header className="absolute inset-x-0 top-0 z-30 flex h-16 items-center px-4 text-white sm:px-7"><Brand /><nav className="mx-auto hidden rounded-full bg-black/25 p-1 backdrop-blur lg:flex">{[navItems[1], navItems[0], navItems[2], navItems[3]].map((item) => <button key={item.id} onClick={() => setView(item.id)} className={cn("rounded-full px-4 py-2 text-sm font-semibold", view === item.id ? "bg-white text-zinc-950" : "text-white/75 hover:text-white")}>{item.label}</button>)}</nav><div className="ml-auto rounded-full bg-black/25 backdrop-blur"><HeaderTools dark={dark} toggle={toggle} onSettings={onSettings} onOnboarding={onOnboarding} /></div></header>{view === "events" ? <main><section className="relative h-[66vh] min-h-[520px] overflow-hidden bg-zinc-950"><img src={photos[0].src} alt="Summer reunion" className="size-full object-cover opacity-85" /><div className="absolute inset-0 bg-gradient-to-t from-zinc-950 via-black/10 to-black/45" /><div className="absolute inset-x-0 bottom-0 mx-auto max-w-7xl p-5 pb-12 text-white sm:p-10"><Badge className="bg-white/15 text-white"><Sparkles className="size-3" />{recent.length ? "New Event" : "Featured Event"}</Badge><h1 className="mt-4 max-w-3xl text-4xl font-bold sm:text-6xl">Summer reunion</h1><p className="mt-3 text-white/75">June 14–16 · 24 photos and videos shared with you</p><div className="mt-6 flex gap-2"><Button className="bg-white text-zinc-950 hover:bg-white/90" onClick={() => onOpen(photos[0])}><Images className="size-4" />Open Event</Button><Button className="border-white/25 bg-black/20 text-white hover:bg-black/35" variant="outline"><Download className="size-4" />Download</Button></div></div></section><section className="relative z-10 mx-auto -mt-4 max-w-7xl rounded-t-[2rem] bg-background px-4 py-9 sm:px-8"><div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_350px]"><div><div className="mb-5 flex items-end justify-between"><div><h2 className="text-2xl font-bold">Your Events</h2><p className="mt-1 text-sm text-muted-foreground">Family stories arranged for you</p></div><button className="text-sm font-semibold text-primary">See all</button></div><div className="grid gap-4 sm:grid-cols-2">{[{ title: "Grandma's 80th", photo: photos[7], date: "May 28 · 31 photos" }, { title: "Spring break", photo: photos[11], date: "April 11–12 · 42 photos" }].map((event) => <button key={event.title} onClick={() => setView("events")} className="group relative overflow-hidden rounded-3xl text-left"><img src={event.photo.src} alt="" className="aspect-[4/3] w-full object-cover transition duration-500 group-hover:scale-105" /><div className="absolute inset-0 bg-gradient-to-t from-black/80 via-transparent to-transparent" /><div className="absolute bottom-0 p-5 text-white"><h3 className="text-xl font-bold">{event.title}</h3><p className="text-sm text-white/70">{event.date}</p></div></button>)}</div><div className="mt-9"><div className="mb-4 flex items-center justify-between"><h2 className="text-xl font-bold">Shared separately</h2><button onClick={() => setView("photos")} className="text-sm font-semibold text-primary">View timeline</button></div><div className="grid grid-cols-2 gap-2 sm:grid-cols-4">{photos.filter((photo) => photo.loose).map((photo) => <PhotoTile key={photo.id} photo={photo} onOpen={onOpen} className="aspect-square" />)}</div></div></div><aside><div className="mb-4 flex items-center justify-between"><h2 className="font-bold">Recently published</h2>{recent.length > 0 && <span className="text-xs font-semibold text-primary">{recent.length} new</span>}</div><NewForYou seen={seen} onSeen={onSeen} onOpen={onOpen} style="compact" /></aside></div></section></main> : <main className="mx-auto max-w-7xl px-4 pb-10 pt-24">{view === "photos" ? <><div className="mb-7"><h1 className="text-3xl font-bold">Photos</h1><p className="mt-1 text-muted-foreground">Everything shared with you, by date</p></div><Timeline onOpen={onOpen} /></> : <CommonView view={view} onOpen={onOpen} />}</main>}<BottomNav view={view} setView={setView} eventFirst /></div>;
}

export default function App() {
  const { variant, setVariant } = useVariant();
  const { dark, toggle } = useTheme();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [onboardingOpen, setOnboardingOpen] = useState(false);
  const [selectedPhoto, setSelectedPhoto] = useState<Photo | null>(() => { const id = Number(new URLSearchParams(window.location.search).get("photo")); return photos.find((photo) => photo.id === id) ?? null; });
  const [seen, setSeen] = useState<Set<string>>(() => new Set());
  const props: VariantProps = useMemo(() => ({
    dark,
    toggle,
    onSettings: () => setSettingsOpen(true),
    onOnboarding: () => setOnboardingOpen(true),
    onOpen: (photo: Photo) => setSelectedPhoto(photo),
    seen,
    onSeen: (id: string) => setSeen((current) => new Set(current).add(id)),
  }), [dark, seen]);
  return <>{variant === "A" && <VariantA {...props} />}{variant === "B" && <VariantB {...props} />}{variant === "C" && <VariantC {...props} />}<MediaViewer photo={selectedPhoto} open={selectedPhoto !== null} onOpenChange={(open) => !open && setSelectedPhoto(null)} /><SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} /><OnboardingDialog open={onboardingOpen} onOpenChange={setOnboardingOpen} /><PrototypeSwitcher variant={variant} setVariant={setVariant} /></>;
}
