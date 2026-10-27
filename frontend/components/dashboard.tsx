"use client";

import { Fragment, useCallback, useEffect, useId, useRef, useState, useSyncExternalStore, type FormEvent } from "react";
import { MorphIcon, type IconNode } from "morphicons/react";
import { ArrowDownToLineIcon, FileIcon, UploadIcon, ChevronRightIcon, ExternalLinkIcon, FolderIcon, FolderOpenIcon, PauseIcon, PlayIcon, RotateCcwIcon } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Breadcrumb, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator } from "@/components/ui/breadcrumb";
import { Button } from "@/components/ui/button";
import { Card, CardPanel } from "@/components/ui/card";
import { Collapsible, CollapsiblePanel, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Dialog, DialogClose, DialogDescription, DialogFooter, DialogHeader, DialogPanel, DialogPopup, DialogTitle } from "@/components/ui/dialog";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Frame, FrameHeader, FramePanel, FrameTitle } from "@/components/ui/frame";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress, ProgressIndicator, ProgressTrack } from "@/components/ui/progress";
import { RadioGroupPrimitive, RadioPrimitive } from "@/components/ui/radio-group";
import { SegmentedControl, SegmentedControlItem } from "@/components/ui/segmented-control";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { ToastProvider, toastManager } from "@/components/ui/toast";
import { Tooltip, TooltipPopup, TooltipTrigger } from "@/components/ui/tooltip";
import { api, bytes, duration, folderID, folderUrl, message, parseConfig, parseFolders, parseJobs, rate, record, type Folder, type Job, type Sample, type RuntimeConfig } from "@/lib/transfers";

const states: Record<string, string> = { running: "Transferring", queued: "Queued", retry_wait: "Waiting to retry", paused: "Paused", failed: "Failed", complete: "Verified" };
const badgeVariant: Record<string, "success" | "info" | "warning" | "error" | "secondary"> = { running: "info", complete: "success", retry_wait: "warning", failed: "error" };

// Release names often have no spaces. Allow line breaks after dots and dashes instead of mid-word.
function breakable(name: string) {
  return name.split(/(?<=[.\-_])/).map((part, index) => <span key={index}>{part}<wbr /></span>);
}

function newTab(href: string) {
  return <a href={href} target="_blank" rel="noopener noreferrer" />;
}

function NewTabHint() {
  return <span className="sr-only"> (opens in a new tab)</span>;
}

// Lucide's sun and moon as icon data, so morphicons can morph between them.
const SUN_ICON: IconNode = [
  ["circle", { cx: "12", cy: "12", r: "4" }],
  ["path", { d: "M12 2v2" }], ["path", { d: "M12 20v2" }],
  ["path", { d: "m4.93 4.93 1.41 1.41" }], ["path", { d: "m17.66 17.66 1.41 1.41" }],
  ["path", { d: "M2 12h2" }], ["path", { d: "M20 12h2" }],
  ["path", { d: "m6.34 17.66-1.41 1.41" }], ["path", { d: "m19.07 4.93-1.41 1.41" }],
];
const X_ICON: IconNode = [["path", { d: "M18 6 6 18" }], ["path", { d: "m6 6 12 12" }]];
const CHECK_ICON: IconNode = [["path", { d: "M20 6 9 17l-5-5" }]];
const TRASH_ICON: IconNode = [
  ["path", { d: "M10 11v6" }], ["path", { d: "M14 11v6" }],
  ["path", { d: "M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" }], ["path", { d: "M3 6h18" }],
  ["path", { d: "M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" }],
];
const MOON_ICON: IconNode = [["path", { d: "M20.985 12.486a9 9 0 1 1-9.473-9.472c.405-.022.617.46.402.803a6 6 0 0 0 8.268 8.268c.344-.215.825-.004.803.401" }]];

function StaticIcon({ icon, className }: { icon: IconNode; className: string }) {
  return <svg className={className} aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">{icon.map(([Tag, attrs], index) => <Tag key={index} {...attrs} />)}</svg>;
}

const noSubscribe = () => () => {};

function ThemeToggle() {
  const mounted = useSyncExternalStore(noSubscribe, () => true, () => false);
  const [dark, setDark] = useState<boolean | null>(null);
  useEffect(() => { setDark(document.documentElement.classList.contains("dark")); }, []);
  function toggle() {
    const next = !dark;
    setDark(next); document.documentElement.classList.toggle("dark", next);
    try { localStorage.setItem("torrent-drive-theme", next ? "dark" : "light"); } catch { /* The theme still applies for this visit. */ }
  }
  const label = dark ? "Switch to light theme" : "Switch to dark theme";
  return <Tooltip>
    <TooltipTrigger render={<Button variant="ghost" size="icon" onClick={toggle} aria-label={label} />}>
      {mounted && dark !== null
        // First client render shows the current icon statically; later toggles morph.
        ? <MorphIcon icon={dark ? SUN_ICON : MOON_ICON} size={16} reducedMotion="user" />
        // The theme is unknown before hydration, so CSS picks the icon.
        : <><StaticIcon icon={SUN_ICON} className="hidden dark:block" /><StaticIcon icon={MOON_ICON} className="dark:hidden" /></>}
    </TooltipTrigger>
    <TooltipPopup>{dark ? "Light theme" : "Dark theme"}</TooltipPopup>
  </Tooltip>;
}

function SiteHeader({ config }: { config: RuntimeConfig }) {
  return <header className="sticky top-0 z-20 px-4 pt-4">
    <div className="mx-auto flex w-full max-w-6xl items-center justify-between rounded-2xl border bg-background/70 px-5 py-3 shadow-sm backdrop-blur-md">
      <a className="flex items-center gap-2.5 rounded-md font-semibold text-base outline-none focus-visible:ring-2 focus-visible:ring-ring" href="/"><img src="/favicon.svg" width="28" height="28" alt="" className="brightness-0 dark:invert" />Torrent Drive</a>
      <nav aria-label="Primary" className="flex items-center gap-1">
        <Button variant="ghost" size="sm" className="max-sm:hidden" render={newTab(folderUrl(config.default_folder))}><FolderOpenIcon aria-hidden="true" />Drive folder<NewTabHint /></Button>
        {config.qbittorrent_url && <Button variant="ghost" size="sm" className="max-sm:hidden" render={newTab(config.qbittorrent_url)}>qBittorrent<NewTabHint /></Button>}
        <ThemeToggle />
      </nav>
    </div>
  </header>;
}

function useTransfers() {
  const [data, setData] = useState<{ jobs: Job[] | null; speeds: Record<string, number | null>; connected: boolean; error: string }>({ jobs: null, speeds: {}, connected: false, error: "" });
  const samples = useRef(new Map<string, Sample[]>());
  const inFlight = useRef(false);
  const controller = useRef<AbortController | null>(null);
  const reload = useCallback(async () => {
    if (inFlight.current || document.hidden) return;
    inFlight.current = true;
    const request = new AbortController(); controller.current = request;
    try {
      const jobs = parseJobs(await api("/jobs", undefined, request.signal));
      const speeds: Record<string, number | null> = {};
      for (const job of jobs) {
        const result = rate(samples.current.get(job.id) ?? [], { time: Date.now(), uploaded: job.uploaded_bytes });
        samples.current.set(job.id, result.history); speeds[job.id] = result.speed;
      }
      for (const id of samples.current.keys()) if (!jobs.some(job => job.id === id)) samples.current.delete(id);
      setData({ jobs, speeds, connected: true, error: "" });
    } catch (error) {
      if (!request.signal.aborted) setData(previous => ({ ...previous, connected: false, error: message(error) }));
    } finally { inFlight.current = false; }
  }, []);
  useEffect(() => {
    void reload();
    const timer = setInterval(() => { void reload(); }, 3000);
    const visible = () => { if (!document.hidden) void reload(); };
    document.addEventListener("visibilitychange", visible);
    return () => { clearInterval(timer); controller.current?.abort(); document.removeEventListener("visibilitychange", visible); };
  }, [reload]);
  return { ...data, reload };
}

// Pause, resume, and retry share one request shape. The button reflects the job's current state.
function JobAction({ job, reload, connected }: { job: Job; reload: () => Promise<void>; connected: boolean }) {
  const [pending, setPending] = useState(false);
  const resumable = job.status === "paused" || job.status === "failed";
  const text = resumable ? (job.status === "failed" ? "Retry" : "Resume") : "Pause";
  const Icon = resumable ? (job.status === "failed" ? RotateCcwIcon : PlayIcon) : PauseIcon;
  async function perform() {
    setPending(true);
    const name = job.name || "The torrent";
    try {
      await api(`/jobs/${encodeURIComponent(job.id)}/${resumable ? "retry" : "pause"}`, {}); await reload();
      toastManager.add({ type: "success", title: resumable ? "Transfer queued" : "Transfer paused", description: resumable ? `${name} starts when the downloader is ready.` : `${name} keeps its verified progress.` });
    }
    catch (failure) { toastManager.add({ type: "error", title: `Cannot ${text.toLowerCase()} transfer`, description: message(failure) }); }
    finally { setPending(false); }
  }
  return <Button variant="outline" size="sm" className="shrink-0" loading={pending} disabled={!connected} onClick={perform} aria-label={`${text} ${job.name || "torrent"}`}><Icon aria-hidden="true" />{text}</Button>;
}

// Clearing removes the job from this list only. The files stay in Google Drive.
function ClearJob({ job, reload, connected }: { job: Job; reload: () => Promise<void>; connected: boolean }) {
  const [pending, setPending] = useState(false);
  async function clear() {
    setPending(true);
    try { await api(`/jobs/${encodeURIComponent(job.id)}/clear`, {}); await reload(); }
    catch (failure) { toastManager.add({ type: "error", title: "Cannot clear transfer", description: message(failure) }); setPending(false); }
  }
  return <Tooltip>
    <TooltipTrigger render={<Button variant="ghost" size="icon-sm" disabled={!connected || pending} onClick={clear} aria-label={`Clear ${job.name || "transfer"} from the list`} />}>
      <MorphIcon icon={pending ? CHECK_ICON : X_ICON} size={16} reducedMotion="user" />
    </TooltipTrigger>
    <TooltipPopup>Clear from list</TooltipPopup>
  </Tooltip>;
}

// The first click asks for confirmation. The second click clears all completed jobs.
function ClearAll({ reload, connected }: { reload: () => Promise<void>; connected: boolean }) {
  const [confirming, setConfirming] = useState(false);
  const [pending, setPending] = useState(false);
  useEffect(() => {
    if (!confirming || pending) return;
    const timer = setTimeout(() => setConfirming(false), 4000);
    return () => clearTimeout(timer);
  }, [confirming, pending]);
  async function clear() {
    if (!confirming) { setConfirming(true); return; }
    setPending(true);
    try { await api("/jobs/clear-completed", {}); await reload(); toastManager.add({ type: "success", title: "List cleared", description: "The files stay in Google Drive." }); }
    catch (failure) { toastManager.add({ type: "error", title: "Cannot clear transfers", description: message(failure) }); }
    finally { setPending(false); setConfirming(false); }
  }
  return <Button variant={confirming ? "destructive-outline" : "ghost"} size="sm" disabled={!connected || pending} onClick={clear} onBlur={() => { if (!pending) setConfirming(false); }}>
    <MorphIcon icon={confirming ? CHECK_ICON : TRASH_ICON} size={16} reducedMotion="user" />
    {confirming ? "Confirm clear" : "Clear all"}
  </Button>;
}

function JobDetails({ job }: { job: Job }) {
  return <Collapsible>
    <CollapsibleTrigger className="group flex min-h-8 items-center gap-1 rounded-md text-muted-foreground text-sm outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
      <ChevronRightIcon aria-hidden="true" className="size-4 transition-transform group-data-panel-open:rotate-90 motion-reduce:transition-none" />Details
    </CollapsibleTrigger>
    <CollapsiblePanel>
      <dl className="mt-2 grid gap-2 text-sm">
        <div className="grid grid-cols-[6rem_minmax(0,1fr)] gap-2"><dt className="text-muted-foreground">Job ID</dt><dd><code className="break-all font-mono text-xs">{job.id}</code></dd></div>
        <div className="grid grid-cols-[6rem_minmax(0,1fr)] gap-2"><dt className="text-muted-foreground">Destination</dt><dd><a href={folderUrl(job.folder)} target="_blank" rel="noopener noreferrer" className="font-medium text-brand underline-offset-4 hover:underline">Open Drive folder<NewTabHint /></a></dd></div>
        {job.results?.map(file => <div key={file.drive_id} className="grid grid-cols-[6rem_minmax(0,1fr)] gap-2"><dt className="text-muted-foreground">SHA-256</dt><dd><span className="block break-words">{file.path}</span><code className="block break-all font-mono text-muted-foreground text-xs">{file.sha256}</code></dd></div>)}
      </dl>
    </CollapsiblePanel>
  </Collapsible>;
}

function CurrentTransfer({ job, speed, reload, connected }: { job: Job; speed: number | null; reload: () => Promise<void>; connected: boolean }) {
  const verifying = job.total_bytes > 0 && job.uploaded_bytes >= job.total_bytes;
  const percent = job.total_bytes ? Math.min(100, job.uploaded_bytes / job.total_bytes * 100) : 0;
  const rateLabel = !connected ? "Reconnecting" : verifying ? "Checking file contents" : !job.files ? "Fetching metadata" : speed === null ? "Measuring" : speed > 0 ? `${bytes(speed)}/s` : "Waiting for peers";
  const remaining = connected && speed && speed > 0 && !verifying ? `~ ${duration((job.total_bytes - job.uploaded_bytes) / speed)}` : verifying ? "Final check" : "Calculating";
  return <Card render={<article aria-labelledby={`name-${job.id}`} />}>
    <CardPanel className="p-6 sm:p-7">
      <div className="flex items-start justify-between gap-3">
        <Badge variant="info" size="lg">{verifying ? "Verifying" : "Transferring"}</Badge>
        <JobAction job={job} reload={reload} connected={connected} />
      </div>
      <h3 id={`name-${job.id}`} className="mt-4 font-semibold text-lg/snug">{job.name ? breakable(job.name) : "Resolving torrent metadata"}</h3>
      <div className="mt-6 mb-3 flex items-end justify-between gap-3 tabular-nums">
        <p className="text-sm">{job.total_bytes ? <><strong className="font-medium">{bytes(job.uploaded_bytes)}</strong> <span className="text-muted-foreground">of {bytes(job.total_bytes)}</span></> : <span className="text-muted-foreground">Waiting for torrent metadata</span>}</p>
        <p className="font-semibold text-4xl/none tracking-tight">{job.total_bytes ? <>{percent.toFixed(1)}<span className="ml-0.5 font-normal text-base text-muted-foreground">%</span></> : "—"}</p>
      </div>
      <Progress value={percent} aria-label={`Upload progress for ${job.name || "torrent"}`}><ProgressTrack className="h-2"><ProgressIndicator className="bg-brand" /></ProgressTrack></Progress>
      <dl className="mt-6 grid grid-cols-3 gap-4 tabular-nums">
        <div><dt className="text-muted-foreground text-xs">Upload speed</dt><dd className="mt-1 font-medium text-sm">{rateLabel}</dd></div>
        <div><dt className="text-muted-foreground text-xs">Time left</dt><dd className="mt-1 font-medium text-sm">{remaining}</dd></div>
        <div><dt className="text-muted-foreground text-xs">Files verified</dt><dd className="mt-1 font-medium text-sm">{job.verified_files} <span className="font-normal text-muted-foreground">/ {job.files}</span></dd></div>
      </dl>
      {job.error && <p className="mt-4 text-destructive-foreground text-sm">{job.error}</p>}
      <div className="mt-5 border-t pt-3"><JobDetails job={job} /></div>
    </CardPanel>
  </Card>;
}

function JobRow({ job, reload, connected }: { job: Job; reload: () => Promise<void>; connected: boolean }) {
  const complete = job.status === "complete";
  const percent = job.total_bytes ? Math.min(100, job.uploaded_bytes / job.total_bytes * 100) : 0;
  const retryTime = job.status === "retry_wait" && job.retry_at && !job.retry_at.startsWith("0001") ? new Date(job.retry_at) : null;
  const target = driveTarget(job);
  return <FramePanel className="p-4"><article aria-labelledby={`name-${job.id}`}>
    <div className="flex items-start justify-between gap-4">
      <div className="min-w-0">
        <h3 id={`name-${job.id}`} className="font-medium text-sm/snug">{job.name ? breakable(job.name) : "Resolving torrent metadata"}</h3>
        <p className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-muted-foreground text-xs tabular-nums">
          <Badge variant={badgeVariant[job.status] ?? "secondary"}>{states[job.status] ?? "Unknown state"}</Badge>
          {job.total_bytes > 0 && <span>{complete ? bytes(job.total_bytes) : `${bytes(job.uploaded_bytes)} of ${bytes(job.total_bytes)}`}</span>}
          {job.files > 0 && <span>· {job.verified_files} of {job.files} {job.files === 1 ? "file" : "files"} verified</span>}
        </p>
      </div>
      {complete
        ? <div className="flex shrink-0 items-center gap-1">
            <Button variant="outline" size="sm" render={newTab(target)}><ExternalLinkIcon aria-hidden="true" />Open<span className="sr-only"> {job.name} in Google Drive</span><NewTabHint /></Button>
            <ClearJob job={job} reload={reload} connected={connected} />
          </div>
        : <JobAction job={job} reload={reload} connected={connected} />}
    </div>
    {!complete && job.total_bytes > 0 && <Progress value={percent} className="mt-3" aria-label={`Upload progress for ${job.name || "torrent"}`}><ProgressTrack className="h-1"><ProgressIndicator className="bg-brand" /></ProgressTrack></Progress>}
    {job.error && <p className="mt-2 text-destructive-foreground text-sm">{job.error}</p>}
    {retryTime && !Number.isNaN(retryTime.valueOf()) && <p className="mt-2 text-muted-foreground text-xs">Next retry at {retryTime.toLocaleTimeString()}.</p>}
    <div className="mt-2"><JobDetails job={job} /></div>
  </article></FramePanel>;
}

function JobSection({ title, jobs, reload, connected, clearable = false }: { title: string; jobs: Job[]; reload: () => Promise<void>; connected: boolean; clearable?: boolean }) {
  const id = useId();
  return <section aria-labelledby={id}>
    <Frame>
      <FrameHeader className="flex-row items-center justify-between px-4 py-2.5"><FrameTitle><h2 id={id}>{title}</h2></FrameTitle><div className="flex items-center gap-3"><span className="text-muted-foreground text-sm tabular-nums">{jobs.length}</span>{clearable && <ClearAll reload={reload} connected={connected} />}</div></FrameHeader>
      {jobs.map(job => <JobRow key={job.id} job={job} reload={reload} connected={connected} />)}
    </Frame>
  </section>;
}

function FolderPicker({ open, onOpenChange, choose }: { open: boolean; onOpenChange: (open: boolean) => void; choose: (folder: Folder, name: string) => void }) {
  const [trail, setTrail] = useState<Folder[]>([{ id: "root", name: "My Drive" }]);
  const [data, setData] = useState<{ files: Folder[]; loading: boolean; error: string; truncated: boolean }>({ files: [], loading: true, error: "", truncated: false });
  const [attempt, setAttempt] = useState(0);
  const [pasted, setPasted] = useState("");
  const [pasteError, setPasteError] = useState("");
  const current = trail[trail.length - 1];
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setData({ files: [], loading: true, error: "", truncated: false });
    api(`/folders?parent=${encodeURIComponent(current.id)}`, undefined, controller.signal).then(parseFolders).then(result => {
      setData({ ...result, loading: false, error: "" });
    }).catch(error => { if (!controller.signal.aborted) setData({ files: [], loading: false, error: message(error), truncated: false }); });
    return () => controller.abort();
  }, [open, current.id, attempt]);
  function submitPasted(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const id = folderID(pasted);
    if (!id) { setPasteError("Enter a Drive folder URL or folder ID."); return; }
    choose({ id, name: "Selected Drive folder" }, "Selected Drive folder");
  }
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogPopup className="sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>Choose a Drive folder</DialogTitle>
        <DialogDescription>Open a folder, then save the transfer there.</DialogDescription>
      </DialogHeader>
      <DialogPanel className="grid gap-3">
        <Breadcrumb>
          <BreadcrumbList>
            {trail.map((folder, index) => index === trail.length - 1
              ? <BreadcrumbItem key={folder.id}><BreadcrumbPage>{folder.name}</BreadcrumbPage></BreadcrumbItem>
              : <Fragment key={folder.id}><BreadcrumbItem><BreadcrumbLink render={<button type="button" onClick={() => setTrail(trail.slice(0, index + 1))} />}>{folder.name}</BreadcrumbLink></BreadcrumbItem><BreadcrumbSeparator /></Fragment>)}
          </BreadcrumbList>
        </Breadcrumb>
        <div className="min-h-48 rounded-lg border" aria-busy={data.loading}>
          {data.loading
            ? <div className="grid gap-1 p-1" role="status" aria-label="Loading Drive folders">{[0, 1, 2, 3].map(index => <Skeleton key={index} className="h-9 rounded-md" />)}</div>
            : data.error
              ? <Empty className="py-8"><EmptyHeader><EmptyTitle>Drive folders did not load</EmptyTitle><EmptyDescription>{data.error}</EmptyDescription></EmptyHeader><EmptyContent><Button variant="outline" size="sm" onClick={() => setAttempt(attempt + 1)}>Try again</Button></EmptyContent></Empty>
              : data.files.length
                ? <ul className="grid grid-cols-1 gap-0.5 p-1">{data.files.map(folder => <li key={folder.id} className="min-w-0">
                    <Button variant="ghost" className="w-full min-w-0 justify-start font-normal" title={folder.name} onClick={() => setTrail([...trail, folder])}><FolderIcon aria-hidden="true" className="text-muted-foreground" /><span className="min-w-0 flex-1 truncate text-left">{folder.name}</span><ChevronRightIcon aria-hidden="true" className="text-muted-foreground" /></Button>
                  </li>)}</ul>
                : <Empty className="py-8"><EmptyHeader><EmptyTitle>No subfolders</EmptyTitle><EmptyDescription>Save to {current.name}, or go back.</EmptyDescription></EmptyHeader></Empty>}
        </div>
        {data.truncated && <p className="text-muted-foreground text-xs">Showing the first 1,000 folders. Paste a folder URL if yours is missing.</p>}
        <Collapsible>
          <CollapsibleTrigger className="group flex min-h-8 items-center gap-1 rounded-md text-muted-foreground text-sm outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
            <ChevronRightIcon aria-hidden="true" className="size-4 transition-transform group-data-panel-open:rotate-90 motion-reduce:transition-none" />Paste a folder URL
          </CollapsibleTrigger>
          <CollapsiblePanel>
            <form onSubmit={submitPasted} noValidate className="mt-2 flex items-start gap-2">
              <Field invalid={Boolean(pasteError)} className="flex-1">
                <FieldLabel className="sr-only">Folder URL or ID</FieldLabel>
                <Input value={pasted} onChange={event => { setPasted(event.target.value); setPasteError(""); }} placeholder="https://drive.google.com/drive/folders/…" autoComplete="off" spellCheck={false} className="font-mono" />
                {pasteError && <FieldError match>{pasteError}</FieldError>}
              </Field>
              <Button type="submit" variant="outline">Use URL</Button>
            </form>
          </CollapsiblePanel>
        </Collapsible>
      </DialogPanel>
      <DialogFooter>
        <DialogClose render={<Button variant="ghost" />}>Cancel</DialogClose>
        <Button disabled={current.id === "root" || data.loading} onClick={() => choose(current, trail.slice(1).map(f => f.name).join(" / "))}>
          {current.id === "root" ? "Open a folder first" : `Save to ${current.name}`}
        </Button>
      </DialogFooter>
    </DialogPopup>
  </Dialog>;
}

function fileData(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => typeof reader.result === "string" ? resolve(reader.result.split(",", 2)[1]) : reject(new Error("The torrent file could not be read."));
    reader.onerror = () => reject(new Error("The torrent file could not be read."));
    reader.onabort = () => reject(new Error("Reading the torrent file was cancelled."));
    reader.readAsDataURL(file);
  });
}

function SubmissionForm({ reload, defaultFolder }: { reload: () => Promise<void>; defaultFolder: string }) {
  const [mode, setMode] = useState("magnet");
  const [magnet, setMagnet] = useState("");
  const [folder, setFolder] = useState(defaultFolder);
  const [folderName, setFolderName] = useState(defaultFolder ? "Default Drive folder" : "Choose a folder");
  const [picking, setPicking] = useState(false);
  const [discovery, setDiscovery] = useState(false);
  const [pending, setPending] = useState(false);
  const [sourceError, setSourceError] = useState("");
  const [submitError, setSubmitError] = useState("");
  const magnetRef = useRef<HTMLTextAreaElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [dragging, setDragging] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const chooseRef = useRef<HTMLButtonElement>(null);
  const discoveryId = useId();
  function pick(chosen: File | undefined) {
    setFile(chosen ?? null); setSourceError("");
    if (fileRef.current) fileRef.current.value = "";
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (pending) return;
    setSourceError(""); setSubmitError("");
    const id = folderID(folder);
    if (!id) { setSubmitError("Choose a Drive folder."); setPicking(true); return; }
    if (mode === "magnet" && (!magnet.trim().startsWith("magnet:?") || magnet.trim().length > 16384)) {
      setSourceError("Enter a magnet link that starts with magnet:? (up to 16 KiB)."); magnetRef.current?.focus(); return;
    }
    if (mode === "file" && (!file || !file.size || file.size > 8 * 1024 * 1024)) {
      setSourceError("Choose a .torrent file, up to 8 MiB."); chooseRef.current?.focus(); return;
    }
    setPending(true);
    try {
      const source = mode === "file" && file ? { torrent: await fileData(file) } : { magnet: magnet.trim() };
      const job = await api("/jobs", { folder: id, public_discovery: discovery, ...source });
      if (!record(job) || typeof job.id !== "string") throw new Error("The submission returned no job ID.");
      setMagnet(""); setFile(null); setDiscovery(false);
      toastManager.add({ type: "success", title: "Transfer added", description: `Files go to ${folderName}.` });
      await reload();
    } catch (error) { setSubmitError(`${message(error)} Check your transfers before you submit again.`); }
    finally { setPending(false); }
  }
  return <Card render={<aside id="add-torrent" aria-labelledby="add-heading" />} className="scroll-mt-24 lg:sticky lg:top-24">
    <CardPanel className="p-6">
      <h2 id="add-heading" className="font-semibold text-base">New transfer</h2>
      <p className="mt-1 text-muted-foreground text-sm">Download a torrent straight to Google Drive.</p>
      <form onSubmit={submit} aria-labelledby="add-heading" aria-busy={pending} noValidate className="mt-5 grid gap-5">
        <SegmentedControl aria-label="Torrent source" className="w-full" render={<RadioGroupPrimitive value={mode} onValueChange={value => { setMode(String(value)); setSourceError(""); }} disabled={pending} />}>
          <SegmentedControlItem className="grow" render={<RadioPrimitive.Root value="magnet" />}>Magnet link</SegmentedControlItem>
          <SegmentedControlItem className="grow" render={<RadioPrimitive.Root value="file" />}>Torrent file</SegmentedControlItem>
        </SegmentedControl>

        {mode === "magnet"
          ? <Field invalid={Boolean(sourceError)} disabled={pending}>
              <FieldLabel>Magnet link</FieldLabel>
              <Textarea ref={magnetRef} rows={4} value={magnet} onChange={event => { setMagnet(event.target.value); setSourceError(""); }} placeholder="magnet:?xt=urn:btih:…" autoComplete="off" autoCapitalize="off" spellCheck={false} className="font-mono text-xs" />
              {sourceError ? <FieldError match>{sourceError}</FieldError> : <FieldDescription>For private trackers, a torrent file avoids metadata delays.</FieldDescription>}
            </Field>
          : <Field invalid={Boolean(sourceError)} disabled={pending}>
              <FieldLabel>Torrent file</FieldLabel>
              {/* The native input stays hidden. Its button text follows the browser language, not the page. */}
              <input ref={fileRef} type="file" accept=".torrent,application/x-bittorrent" className="sr-only" tabIndex={-1} aria-hidden="true" onChange={event => pick(event.target.files?.[0])} />
              {file
                ? <div className="flex w-full items-center gap-3 rounded-lg border bg-muted/40 p-3">
                    <FileIcon aria-hidden="true" className="size-4 shrink-0 text-brand" />
                    <span className="grid min-w-0 flex-1 gap-0.5 leading-snug"><span className="truncate font-medium text-sm">{file.name}</span><span className="text-muted-foreground text-xs tabular-nums">{bytes(file.size)}</span></span>
                    <Tooltip>
                      <TooltipTrigger render={<Button type="button" variant="ghost" size="icon-sm" disabled={pending} onClick={() => { pick(undefined); chooseRef.current?.focus(); }} aria-label={`Remove ${file.name}`} />}>
                        <MorphIcon icon={X_ICON} size={16} reducedMotion="user" />
                      </TooltipTrigger>
                      <TooltipPopup>Remove file</TooltipPopup>
                    </Tooltip>
                  </div>
                : <div
                    className={`grid w-full justify-items-center gap-2 rounded-lg border border-dashed p-5 text-center transition-colors motion-reduce:transition-none ${dragging ? "border-brand bg-brand/5" : "border-input"}`}
                    onDragOver={event => { event.preventDefault(); setDragging(true); }}
                    onDragLeave={() => setDragging(false)}
                    onDrop={event => { event.preventDefault(); setDragging(false); if (!pending) pick(event.dataTransfer.files[0]); }}
                  >
                    <UploadIcon aria-hidden="true" className="size-5 text-muted-foreground" />
                    <Button ref={chooseRef} type="button" variant="outline" size="sm" disabled={pending} onClick={() => fileRef.current?.click()}>Choose torrent file</Button>
                    <span className="text-muted-foreground text-xs">or drop it here</span>
                  </div>}
              {sourceError ? <FieldError match>{sourceError}</FieldError> : <FieldDescription>A .torrent file, up to 8 MiB.</FieldDescription>}
            </Field>}

        <div className="grid gap-2">
          <span className="font-medium text-sm" id="destination-label">Save to</span>
          <Button variant="outline" size="lg" className="h-auto justify-start py-2.5 text-left sm:h-auto" aria-describedby="destination-label" disabled={pending} onClick={() => setPicking(true)}>
            <FolderIcon aria-hidden="true" className="text-brand" />
            <span className="grid min-w-0 flex-1 gap-0.5 leading-snug"><span className="text-muted-foreground text-xs">Google Drive</span><span className="truncate font-medium">{folderName}</span></span>
            <span className="text-muted-foreground text-xs">Change</span>
          </Button>
          <FolderPicker open={picking} onOpenChange={setPicking} choose={(chosen, name) => { setFolder(chosen.id); setFolderName(name); setPicking(false); setSubmitError(""); }} />
        </div>

        <div className="flex items-start justify-between gap-4 rounded-lg border p-3">
          <div className="grid gap-1">
            <Label htmlFor={discoveryId}>Public peer discovery</Label>
            <p className="text-muted-foreground text-xs">Uses DHT and peer exchange. Turn on only for public magnets, never for private trackers.</p>
          </div>
          <Switch id={discoveryId} checked={discovery} onCheckedChange={setDiscovery} disabled={pending} />
        </div>

        <Button type="submit" size="lg" className="w-full" loading={pending}><ArrowDownToLineIcon aria-hidden="true" />Start transfer</Button>
        {submitError && <Alert variant="error"><AlertDescription>{submitError}</AlertDescription></Alert>}
      </form>
      <p className="mt-5 border-t pt-4 text-muted-foreground text-xs">Up to 256 GiB per torrent. One transfer runs at a time. Data stays in memory until Drive accepts it.</p>
    </CardPanel>
  </Card>;
}

// Placeholders follow the shapes of the real cards, so the layout does not jump when data arrives.
function LoadingTransfers({ error }: { error: string }) {
  return <div className="grid gap-6" role="status" aria-label={error ? "Waiting for the downloader" : "Loading your transfers"}>
    <Card><CardPanel className="grid gap-4 p-6">
      <Skeleton className="h-5.5 w-28 rounded-md" />
      <Skeleton className="h-5 w-3/4" />
      <Skeleton className="h-2 w-full rounded-full" />
      <div className="grid grid-cols-3 gap-4"><Skeleton className="h-9" /><Skeleton className="h-9" /><Skeleton className="h-9" /></div>
    </CardPanel></Card>
    <Frame>
      <FrameHeader className="flex-row items-center justify-between px-4 py-2.5"><Skeleton className="h-5 w-32" /><Skeleton className="h-7 w-20" /></FrameHeader>
      {[0, 1, 2].map(index => <FramePanel key={index} className="flex items-start justify-between gap-4 p-4">
        <div className="grid flex-1 gap-2"><Skeleton className="h-4.5 w-2/3" /><div className="flex gap-2"><Skeleton className="h-4.5 w-16 rounded-sm" /><Skeleton className="h-4.5 w-36" /></div><Skeleton className="h-6 w-20" /></div>
        <div className="flex gap-1"><Skeleton className="h-7 w-18" /><Skeleton className="size-7" /></div>
      </FramePanel>)}
    </Frame>
  </div>;
}

function driveTarget(job: Job) {
  // One verified file opens directly. Multi-file torrents open their Drive folder.
  return job.results?.length === 1 ? `https://drive.google.com/file/d/${encodeURIComponent(job.results[0].drive_id)}/view` : folderUrl(job.folder);
}

// Announce finished and failed transfers. The first load sets the baseline, so old jobs stay quiet.
function useStatusToasts(jobs: Job[] | null) {
  const seen = useRef<Map<string, string> | null>(null);
  useEffect(() => {
    if (!jobs) return;
    const previous = seen.current;
    seen.current = new Map(jobs.map(job => [job.id, job.status]));
    if (!previous) return;
    for (const job of jobs) {
      const before = previous.get(job.id);
      if (before === undefined || before === job.status) continue;
      const name = job.name || "A torrent";
      if (job.status === "complete") toastManager.add({ type: "success", title: "Upload verified", description: `${name} is in Google Drive.`, actionProps: { children: "Open", onClick: () => window.open(driveTarget(job), "_blank", "noopener,noreferrer") } });
      if (job.status === "failed") toastManager.add({ type: "error", title: "Transfer failed", description: job.error ? `${name}: ${job.error}` : `${name} stopped. Retry it from the list.`, timeout: 0 });
    }
  }, [jobs]);
}

export default function Dashboard() {
  const [config, setConfig] = useState<RuntimeConfig | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function load() {
      try { setConfig(parseConfig(await api("/config", undefined, controller.signal))); }
      catch (failure) {
        if (!controller.signal.aborted) { setError(message(failure)); timer = setTimeout(load, 3000); }
      }
    }
    void load();
    return () => { controller.abort(); clearTimeout(timer); };
  }, []);
  if (!config) return <><a className="sr-only focus:not-sr-only" href="#main-content">Skip to content</a><main id="main-content" tabIndex={-1} className="mx-auto max-w-6xl p-8"><h1 className="font-semibold text-2xl">Torrent Drive</h1><p role="status" className="mt-4 text-muted-foreground">{error || "Loading dashboard settings…"}</p><noscript>Enable JavaScript to use the dashboard.</noscript></main></>;
  return <DashboardContent config={config} />;
}

function DashboardContent({ config }: { config: RuntimeConfig }) {
  const { jobs, speeds, connected, error, reload } = useTransfers();
  useStatusToasts(jobs);
  const running = jobs?.find(job => job.status === "running");
  const queued = jobs?.filter(job => job.status !== "complete" && job !== running) ?? [];
  // Newest completed transfers first.
  const completed = jobs?.filter(job => job.status === "complete").reverse() ?? [];
  const active = jobs?.filter(job => job.status !== "complete") ?? [];
  const remaining = active.reduce((sum, job) => sum + Math.max(0, job.total_bytes - job.uploaded_bytes), 0);
  const verified = jobs?.reduce((sum, job) => sum + job.verified_files, 0) ?? 0;
  function focusForm() {
    document.getElementById("add-torrent")?.scrollIntoView({ block: "start" });
    document.querySelector<HTMLElement>("#add-torrent textarea, #add-torrent input[type=file]")?.focus({ preventScroll: true });
  }
  return <ToastProvider>
    <a className="fixed top-3 left-5 z-30 -translate-y-[180%] rounded-lg border-2 border-ring bg-background px-4 py-3 focus:translate-y-0" href="#main-content">Skip to content</a>
    <SiteHeader config={config} />
    <main id="main-content" tabIndex={-1} className="mx-auto w-full max-w-6xl px-5 pt-10 pb-12 outline-none sm:px-8 sm:pt-14">
      <h1 className="font-bold text-4xl tracking-tight sm:text-5xl">Transfers</h1>
      {!jobs && <Skeleton className="mt-3 h-5 w-72 max-w-full" />}
      {jobs && <p className="mt-2 text-muted-foreground tabular-nums">{active.length ? `${active.length} in progress · ${bytes(remaining)} left` : "Nothing in progress"} · {verified} {verified === 1 ? "file" : "files"} verified in Drive</p>}
      <noscript><p className="mt-6 rounded-lg border p-4 text-sm">Enable JavaScript to view progress and submit torrents.</p></noscript>
      {error && <Alert variant="warning" className="mt-6"><AlertDescription>{error} {jobs ? "Showing the last saved progress." : "This page reconnects automatically."}</AlertDescription></Alert>}

      <div className="mt-8 grid items-start gap-8 lg:grid-cols-[minmax(0,1fr)_23rem]">
        <div className="grid min-w-0 gap-6">
          {!jobs
            ? <LoadingTransfers error={error} />
            : running
              ? <section aria-label="Current transfer"><CurrentTransfer job={running} speed={speeds[running.id] ?? null} reload={reload} connected={connected} /></section>
              : <Card><Empty className="py-12">
                  <EmptyHeader>
                    <EmptyMedia variant="icon"><ArrowDownToLineIcon aria-hidden="true" /></EmptyMedia>
                    <EmptyTitle>{queued.length ? "Waiting to start" : "No active transfer"}</EmptyTitle>
                    <EmptyDescription>{queued.length ? "The next transfer starts when the downloader is ready." : "Add a magnet link or a torrent file. The file goes straight to Google Drive."}</EmptyDescription>
                  </EmptyHeader>
                  {!queued.length && <EmptyContent><Button variant="outline" onClick={focusForm}>Add a torrent</Button></EmptyContent>}
                </Empty></Card>}
          {queued.length > 0 && <JobSection title="Up next" jobs={queued} reload={reload} connected={connected} />}
          {completed.length > 0 && <JobSection title="In Google Drive" jobs={completed} reload={reload} connected={connected} clearable />}
          <p className="max-w-prose text-muted-foreground text-sm">A transfer is complete only after Drive confirms the full size and SHA-256 checksum. These transfers run separately from qBittorrent.</p>
        </div>
        <SubmissionForm reload={reload} defaultFolder={config.default_folder} />
      </div>
    </main>
    <footer className="mx-auto flex w-full max-w-6xl flex-col gap-2 border-t px-5 py-6 text-muted-foreground text-sm sm:flex-row sm:justify-end sm:px-8">
      <div className="flex flex-wrap gap-x-5 gap-y-2">
        {config.qbittorrent_url && <a href={config.qbittorrent_url} target="_blank" rel="noopener noreferrer" className="hover:text-foreground hover:underline">qBittorrent<NewTabHint /></a>}
        <a href={folderUrl(config.default_folder)} target="_blank" rel="noopener noreferrer" className="hover:text-foreground hover:underline">Google Drive<NewTabHint /></a>
      </div>
    </footer>
  </ToastProvider>;
}
