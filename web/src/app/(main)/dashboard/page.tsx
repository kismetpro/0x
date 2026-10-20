"use client";

import * as React from "react";

import Link from "next/link";

import {
  ActivityIcon,
  ArrowUpRightIcon,
  BugIcon,
  ClockIcon,
  NetworkIcon,
  ShieldCheckIcon,
  TargetIcon,
  WifiIcon,
  WifiOffIcon,
  ZapIcon,
} from "lucide-react";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";

import { StatusBadge } from "@/components/status-badge";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { type ChartConfig, ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { Progress } from "@/components/ui/progress";
import { api } from "@/lib/api";
import type {
  Activity,
  Agent,
  Finding,
  InterceptPending,
  LLMProfile,
  MCPServer,
  NetworkHistory,
  NetworkState,
  NetworkStatus,
  Settings,
  SkillItem,
  Stats,
  Task,
  Tool,
  TrafficExchange,
  UsageStats,
} from "@/lib/types";
import { cn } from "@/lib/utils";

// ── chart constants ───────────────────────────────────────────────────────────

const dailyTrendConfig = {
  input: { label: "输入(未命中)", color: "hsl(217 91% 60%)" },
  output: { label: "输出", color: "hsl(263 70% 60%)" },
  cacheRead: { label: "缓存读", color: "hsl(160 60% 45%)" },
} satisfies ChartConfig;

// ── helpers ──────────────────────────────────────────────────────────────────

function fmtRel(ts?: string | number): string {
  if (!ts) return "—";
  const ms = Date.now() - (typeof ts === "number" ? ts * 1000 : Date.parse(ts as string));
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s 前`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m 前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h 前`;
  return `${Math.floor(h / 24)}d 前`;
}

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 1000) return (n / 1000).toFixed(1) + "k";
  return String(n);
}

function parseTime(ts?: string | number): Date | null {
  if (ts === undefined || ts === null || ts === "") return null;
  const d = new Date(typeof ts === "number" && Math.abs(ts) < 1_000_000_000_000 ? ts * 1000 : ts);
  return Number.isNaN(d.getTime()) ? null : d;
}

function fmtClock(ts?: string | number): string {
  const d = parseTime(ts);
  if (!d) return "—";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function fmtClockShort(ts?: string | number): string {
  const d = parseTime(ts);
  if (!d) return "—";
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

function fmtDuration(seconds: number): string {
  const total = Math.max(0, Math.round(seconds));
  if (total < 60) return `${total} 秒`;
  const minutes = Math.floor(total / 60);
  if (minutes < 60) return `${minutes} 分 ${total % 60} 秒`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  if (hours < 24) return `${hours} 小时 ${rest} 分`;
  return `${Math.floor(hours / 24)} 天 ${hours % 24} 小时`;
}

const ASSET_TYPE_LABELS: Record<string, string> = {
  root_domain: "根域名",
  ip: "IP",
  subdomain: "子域名",
  app: "应用",
  service: "服务",
  endpoint: "端点",
};

const ASSET_COLORS: Record<string, string> = {
  root_domain: "bg-blue-500",
  ip: "bg-indigo-400",
  subdomain: "bg-violet-500",
  app: "bg-orange-400",
  service: "bg-cyan-500",
  endpoint: "bg-emerald-500",
};

function statusColor(code: number): string {
  if (code < 300) return "text-emerald-500";
  if (code < 400) return "text-blue-400";
  if (code < 500) return "text-amber-400";
  return "text-red-400";
}

function statusBg(code: number): string {
  if (code < 300) return "bg-emerald-500";
  if (code < 400) return "bg-blue-400";
  if (code < 500) return "bg-amber-400";
  return "bg-red-400";
}

function networkMetaFor(state: NetworkState | "waiting" | "stale") {
  switch (state) {
    case "online":
      return {
        label: "网络在线",
        icon: WifiIcon,
        badge: "border-emerald-500/30 bg-emerald-500/10 text-emerald-400",
        dot: "bg-emerald-400",
      };
    case "degraded":
      return {
        label: "网络波动",
        icon: ActivityIcon,
        badge: "border-amber-500/30 bg-amber-500/10 text-amber-400",
        dot: "bg-amber-400",
      };
    case "offline":
      return {
        label: "网络中断",
        icon: WifiOffIcon,
        badge: "border-red-500/30 bg-red-500/10 text-red-400",
        dot: "bg-red-500",
      };
    case "stale":
      return {
        label: "采样超时",
        icon: WifiOffIcon,
        badge: "border-red-500/30 bg-red-500/10 text-red-400",
        dot: "bg-red-500",
      };
    default:
      return {
        label: "等待采样",
        icon: ClockIcon,
        badge: "border-muted-foreground/20 bg-muted/30 text-muted-foreground",
        dot: "bg-muted-foreground/40",
      };
  }
}

// ── sub-components ────────────────────────────────────────────────────────────

function LiveDot({ className }: { className?: string }) {
  return <span className={cn("inline-block size-1.5 shrink-0 animate-pulse rounded-full bg-blue-400", className)} />;
}

function SectionTitle({
  icon: Icon,
  children,
  sub,
}: {
  icon: React.ElementType;
  children: React.ReactNode;
  sub?: React.ReactNode;
}) {
  return (
    <div className="mb-3 flex items-center justify-between">
      <div className="flex items-center gap-1.5 text-xs font-semibold">
        <Icon className="size-3.5 text-muted-foreground" />
        {children}
      </div>
      {sub && <span className="text-[10px] text-muted-foreground">{sub}</span>}
    </div>
  );
}

// ── page ─────────────────────────────────────────────────────────────────────

export default function DashboardPage() {
  // data state
  const [tasks, setTasks] = React.useState<Task[]>([]);
  const [findings, setFindings] = React.useState<Finding[]>([]);
  const [stats, setStats] = React.useState<Stats | null>(null);
  const [settings, setSettings] = React.useState<Settings | null>(null);
  const [pending, setPending] = React.useState<InterceptPending[]>([]);
  const [activity, setActivity] = React.useState<Activity[]>([]);
  const [usageStats, setUsageStats] = React.useState<UsageStats | null>(null);
  const [networkStatus, setNetworkStatus] = React.useState<NetworkStatus | null>(null);
  const [networkHistory, setNetworkHistory] = React.useState<NetworkHistory | null>(null);
  const [networkNow, setNetworkNow] = React.useState<number | null>(null);
  const [assetCounts, setAssetCounts] = React.useState<Record<string, number>>({});
  const [traffic, setTraffic] = React.useState<TrafficExchange[]>([]);
  const [agents, setAgents] = React.useState<Agent[]>([]);
  const [mcpServers, setMcpServers] = React.useState<MCPServer[]>([]);
  const [skills, setSkills] = React.useState<SkillItem[]>([]);
  const [tools, setTools] = React.useState<Tool[]>([]);
  const [llmProfiles, setLLMProfiles] = React.useState<LLMProfile[]>([]);

  // The task list is the most expensive dashboard source. Poll it independently
  // so a large history cannot hold back every other dashboard panel.
  React.useEffect(() => {
    let alive = true;
    let loading = false;
    let signature = "";
    const load = async () => {
      if (loading) return;
      loading = true;
      try {
        const tr = await api.tasks();
        if (!alive) return;
        const nextSignature = JSON.stringify(tr.tasks);
        if (nextSignature !== signature) {
          signature = nextSignature;
          setTasks(tr.tasks);
        }
      } catch {
        /* transient errors — next poll retries */
      } finally {
        loading = false;
      }
    };
    void load();
    const t = setInterval(load, 5000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  // The remaining fast sources stay batched so one poll causes a single render.
  React.useEffect(() => {
    let alive = true;
    let loading = false;
    const load = async () => {
      if (loading) return;
      loading = true;
      try {
        const [findings, stats, settings, pending, activity] = await Promise.all([
          api.findings(),
          api.stats(),
          api.settings(),
          api.interceptPending(),
          api.activity(undefined, { limit: 30 }),
        ]);
        if (!alive) return;
        setFindings(findings);
        setStats(stats);
        setSettings(settings);
        setPending(pending);
        setActivity(activity.items);
      } catch {
        // Preserve stale data and retry on the next interval.
      } finally {
        loading = false;
      }
    };
    void load();
    const timer = setInterval(load, 5000);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, []);

  // Network probing is independent: a slow probe/history query must not hold
  // back findings, approvals, or activity in the five-second dashboard batch.
  React.useEffect(() => {
    let alive = true;
    let loading = false;
    const load = async () => {
      if (loading) return;
      loading = true;
      try {
        const status = await api.networkStatus();
        if (alive) setNetworkStatus(status);
      } catch {
        /* keep the last sample; the next poll retries */
      } finally {
        loading = false;
      }
    };
    void load();
    const timer = setInterval(load, 10000);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, []);

  // slow poll: traffic, assets, system-static, network history (every 15s)
  React.useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const [traf, counts, agentList, mcpList, skillList, toolList, profileList, netHistory] = await Promise.all([
          api.traffic(0, 50),
          api.assetCounts(),
          api.agents(),
          api.mcpServers(),
          api.skills(),
          api.tools(),
          api.llmProfiles(),
          api.networkHistory(24),
        ]);
        if (!alive) return;
        setTraffic(traf.exchanges ?? []);
        setAssetCounts(counts ?? {});
        setAgents(agentList);
        setMcpServers(mcpList);
        setSkills(skillList);
        setTools(toolList);
        setLLMProfiles(profileList);
        setNetworkHistory(netHistory);
        setNetworkNow(Date.now());
      } catch {
        /* transient errors */
      }
    };
    void load();
    const t = setInterval(load, 15000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  // The llm_usage aggregates are much heavier than the other cards and token
  // spend does not need five-second freshness. Polling them every 30 seconds
  // keeps the dashboard responsive while charts still update continuously.
  React.useEffect(() => {
    let alive = true;
    let loading = false;
    const load = async () => {
      if (loading) return;
      loading = true;
      try {
        const usage = await api.usageStats(365);
        if (alive) setUsageStats(usage);
      } catch {
        /* keep the last chart; retry later */
      } finally {
        loading = false;
      }
    };
    void load();
    const timer = setInterval(load, 30000);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, []);

  // ── derived ───────────────────────────────────────────────────────────────

  const tasksByStatus = React.useMemo(() => {
    const m: Record<string, number> = {};
    for (const t of tasks) m[t.status] = (m[t.status] ?? 0) + 1;
    return m;
  }, [tasks]);

  const findingsBySev = React.useMemo(() => {
    const m = { critical: 0, high: 0, medium: 0, low: 0 };
    for (const f of findings) {
      if (f.severity in m) m[f.severity as keyof typeof m]++;
    }
    return m;
  }, [findings]);

  const sortedTasks = React.useMemo(
    () =>
      [...tasks]
        .sort((a, b) => (b.last_activity_unix ?? b.created_unix ?? 0) - (a.last_activity_unix ?? a.created_unix ?? 0))
        .slice(0, 5),
    [tasks],
  );

  const recentFindings = React.useMemo(
    () => [...findings].sort((a, b) => Date.parse(b.ts) - Date.parse(a.ts)).slice(0, 8),
    [findings],
  );

  const recentActivity = React.useMemo(
    () =>
      [...activity]
        .filter((a) => a.kind !== "usage")
        .sort((a, b) => b.seq - a.seq)
        .slice(0, 6),
    [activity],
  );

  const totalAssets = React.useMemo(() => Object.values(assetCounts).reduce((a, b) => a + b, 0), [assetCounts]);

  // asset type breakdown
  const assetByType = React.useMemo(() => {
    return Object.entries(assetCounts)
      .filter(([, n]) => n > 0)
      .sort(([, a], [, b]) => b - a)
      .slice(0, 8);
  }, [assetCounts]);

  const assetMax = assetByType[0]?.[1] ?? 1;

  // traffic status code breakdown
  const trafficByCodes = React.useMemo(() => {
    const m: Record<number, number> = {};
    for (const e of traffic) {
      const bucket = Math.floor(e.status / 100) * 100;
      m[bucket] = (m[bucket] ?? 0) + 1;
    }
    return Object.entries(m)
      .sort(([a], [b]) => Number(a) - Number(b))
      .map(([code, n]) => ({ code: Number(code), n }));
  }, [traffic]);

  const trafficMax = Math.max(...trafficByCodes.map((x) => x.n), 1);
  const recentTraffic = [...traffic].sort((a, b) => Date.parse(b.ts) - Date.parse(a.ts)).slice(0, 5);

  // system
  const activeProfile = llmProfiles.find((p) => p.is_default);
  const enabledTools = tools.filter((t) => t.enabled);
  const pendingCount = pending.length;

  // ── token stats per LLM profile ──────────────────────────────────────────
  // tasks whose llm_profile_id is null/undefined used the active default profile
  const defaultProfileId = activeProfile ? Number(activeProfile.id) : null;

  // 数据源固定为 llm_usage 计量账本：逐次调用精确、含中断消耗。
  // 只覆盖账本启用之后的调用，历史 activity 统计已移除。
  // selected profile tab: "all" = 全部; number = specific profile id
  const [tokenTab, setTokenTab] = React.useState<number | null | "all">("all");
  // 时间范围：默认「今天」（小时级柱状图）；其余选项为日级。
  const [tokenRange, setTokenRange] = React.useState<"today" | 7 | 30 | 90 | 180 | 365>("today");

  // profile 名 → id，用于把 llm_usage 的 profile_name 映射到现有 profile 分栏。
  const profileIdByName = React.useMemo(() => {
    const m = new Map<string, number>();
    for (const p of llmProfiles) m.set(p.name, Number(p.id));
    return m;
  }, [llmProfiles]);

  type Bucket = { input: number; output: number; cacheRead: number; cacheWrite: number; taskCount: number };

  // 按 profile 归桶（来自 llm_usage 全局聚合，逐次调用精确）。
  const tokenByProfile = React.useMemo<Map<number | null, Bucket>>(() => {
    const m = new Map<number | null, Bucket>();
    for (const p of usageStats?.by_profile ?? []) {
      // 未匹配到现有 profile（改名/删除/空名）→ 落到默认桶，仍计入「全部」。
      const key = profileIdByName.get(p.profile_name) ?? defaultProfileId;
      const prev = m.get(key) ?? { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, taskCount: 0 };
      m.set(key, {
        input: prev.input + p.input_tokens,
        output: prev.output + p.output_tokens,
        cacheRead: prev.cacheRead + p.cache_read_tokens,
        cacheWrite: prev.cacheWrite + p.cache_write_tokens,
        taskCount: prev.taskCount + p.tasks,
      });
    }
    return m;
  }, [usageStats, profileIdByName, defaultProfileId]);

  const displayedTokens = React.useMemo(() => {
    if (tokenTab === "all") {
      let input = 0,
        output = 0,
        cacheRead = 0,
        cacheWrite = 0,
        taskCount = 0;
      for (const v of tokenByProfile.values()) {
        input += v.input;
        output += v.output;
        cacheRead += v.cacheRead;
        cacheWrite += v.cacheWrite;
        taskCount += v.taskCount;
      }
      return { input, output, cacheRead, cacheWrite, taskCount };
    }
    const v = tokenByProfile.get(tokenTab as number | null);
    return v
      ? { input: v.input, output: v.output, cacheRead: v.cacheRead, cacheWrite: v.cacheWrite, taskCount: v.taskCount }
      : { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, taskCount: 0 };
  }, [tokenTab, tokenByProfile]);

  // 今天：24 个小时桶（本地时区），默认视图。
  const hourlyTokenData = React.useMemo(() => {
    const today = new Date().toDateString();
    const buckets = Array.from({ length: 24 }, (_, h) => ({
      date: `${String(h).padStart(2, "0")}:00`,
      input: 0,
      output: 0,
      cacheRead: 0,
    }));
    for (const row of usageStats?.hourly ?? []) {
      const d = new Date(row.hour);
      if (Number.isNaN(d.getTime()) || d.toDateString() !== today) continue;
      if (tokenTab !== "all" && (profileIdByName.get(row.profile_name) ?? defaultProfileId) !== tokenTab) continue;
      const b = buckets[d.getHours()];
      const cached = Math.min(row.input_tokens, row.cache_read_tokens);
      b.input += row.input_tokens - cached;
      b.output += row.output_tokens;
      b.cacheRead += cached;
    }
    return buckets;
  }, [usageStats, tokenTab, profileIdByName, defaultProfileId]);

  // 日级范围：来自 llm_usage 的真实每日消耗（ts 是实际调用时刻）。
  const dailyRangeData = React.useMemo(() => {
    const days = tokenRange === "today" ? 1 : tokenRange;
    const now = new Date();
    now.setDate(now.getDate() - days);
    const cutoff = now.toISOString().slice(0, 10);
    const m = new Map<string, { input: number; output: number; cacheRead: number }>();
    for (const d of usageStats?.daily ?? []) {
      if (d.date < cutoff) continue;
      if (tokenTab !== "all" && (profileIdByName.get(d.profile_name) ?? defaultProfileId) !== tokenTab) continue;
      const prev = m.get(d.date) ?? { input: 0, output: 0, cacheRead: 0 };
      const cached = Math.min(d.input_tokens, d.cache_read_tokens);
      m.set(d.date, {
        input: prev.input + d.input_tokens - cached,
        output: prev.output + d.output_tokens,
        cacheRead: prev.cacheRead + cached,
      });
    }
    return [...m.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([date, v]) => ({ date: date.slice(5), ...v }));
  }, [usageStats, tokenRange, tokenTab, profileIdByName, defaultProfileId]);

  const dailyTokenData = tokenRange === "today" ? hourlyTokenData : dailyRangeData;

  // 24h 网络时间条：把后端的分钟桶合并成 5 分钟一格。断网优先于波动，
  // 波动优先于在线，避免短暂故障被同一格里的成功采样掩盖。
  const networkBars = React.useMemo(() => {
    type TimelineState = NetworkHistory["buckets"][number]["state"] | "nodata";
    if (networkNow === null) return [];
    const bucketMs = 5 * 60 * 1000;
    const count = 24 * 12;
    const end = Math.floor(networkNow / bucketMs) * bucketMs;
    const start = end - (count - 1) * bucketMs;
    const bars = Array.from({ length: count }, (_, i) => ({
      ts: start + i * bucketMs,
      state: "nodata" as TimelineState,
      samples: 0,
      latencyTotal: 0,
      latency: 0,
    }));
    const rank: Record<TimelineState, number> = { nodata: 0, online: 1, degraded: 2, offline: 3 };
    for (const bucket of networkHistory?.buckets ?? []) {
      const ts = Date.parse(bucket.ts);
      if (Number.isNaN(ts)) continue;
      const idx = Math.floor((ts - start) / bucketMs);
      if (idx < 0 || idx >= count) continue;
      const bar = bars[idx];
      if (rank[bucket.state] > rank[bar.state]) bar.state = bucket.state;
      bar.samples += bucket.samples;
      bar.latencyTotal += Math.max(0, bucket.latency_ms);
    }
    return bars.map(({ latencyTotal, ...bar }) => ({
      ...bar,
      latency: bar.samples > 0 ? Math.round(latencyTotal / bar.samples) : 0,
    }));
  }, [networkHistory, networkNow]);

  const networkSample = networkStatus?.sample ?? null;
  const networkInterval = networkStatus?.monitor?.interval_seconds ?? 10;
  const networkSampleTime = parseTime(networkSample?.ts);
  const networkSampleAge = networkSampleTime
    ? Math.max(0, Math.floor((Date.now() - networkSampleTime.getTime()) / 1000))
    : null;
  const networkStale = networkSampleAge !== null && networkSampleAge > Math.max(30, networkInterval * 3);
  let networkVisualState: NetworkState | "waiting" | "stale" = "waiting";
  if (networkSample) networkVisualState = networkStale ? "stale" : networkSample.state;
  const networkMeta = networkMetaFor(networkVisualState);
  const NetworkStatusIcon = networkMeta.icon;
  const networkChecks = [
    {
      label: "DNS",
      ok: networkSample ? (networkSample.detail?.dns?.ok ?? networkSample.dns_ok) : null,
      ms: networkSample?.detail?.dns?.ms,
      err: networkSample?.detail?.dns?.err,
    },
    {
      label: "TCP",
      ok: networkSample ? (networkSample.detail?.tcp?.ok ?? networkSample.tcp_ok) : null,
      ms: networkSample?.detail?.tcp?.ms,
      err: networkSample?.detail?.tcp?.err,
    },
    {
      label: "HTTP",
      ok: networkSample ? (networkSample.detail?.http?.ok ?? networkSample.http_ok) : null,
      ms: networkSample?.detail?.http?.ms,
      err: networkSample?.detail?.http?.err,
    },
    {
      label: "ICMP",
      ok: networkSample ? (networkSample.detail?.icmp?.ok ?? networkSample.icmp_ok) : null,
      ms: networkSample?.detail?.icmp?.ms,
      err: networkSample?.detail?.icmp?.err,
    },
    {
      label: "网关",
      ok: networkSample ? (networkSample.detail?.gateway?.ok ?? networkSample.gateway_ok) : null,
      ms: networkSample?.detail?.gateway?.ms,
      err: networkSample?.detail?.gateway?.err,
    },
  ];
  const recentNetworkOutages = [...(networkHistory?.outages ?? [])].slice(-3).reverse();

  // activity kind label
  function kindLabel(a: Activity): string {
    if (a.kind === "tool_use") return a.tool ?? "tool_use";
    if (a.kind === "tool_result") return "tool_result";
    return a.kind;
  }

  function workerColor(w: string): string {
    if (w === "planner") return "text-violet-400";
    if (w === "mainagent") return "text-cyan-400";
    return "text-blue-400";
  }

  function workerBg(w: string): string {
    if (w === "planner") return "bg-violet-500/10 text-violet-400 border-violet-500/20";
    if (w === "mainagent") return "bg-cyan-500/10 text-cyan-400 border-cyan-500/20";
    return "bg-blue-500/10 text-blue-400 border-blue-500/20";
  }

  // ── render ────────────────────────────────────────────────────────────────

  return (
    <div className="flex flex-1 flex-col gap-4 pb-6">
      {/* ── Header ── */}
      <div>
        <div>
          <h1 className="text-lg font-semibold tracking-tight">总览</h1>
          <p className="text-xs text-muted-foreground">系统全局状态 · 实时刷新</p>
        </div>
      </div>

      {/* ── Row 1: 5 stat cards ── */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        {/* 活跃任务 */}
        <Card className="gap-1">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <TargetIcon className="size-3" /> 活跃任务
            </div>
            <div className="flex items-baseline gap-1.5">
              <span className="text-2xl font-semibold tabular-nums">{tasksByStatus.running ?? 0}</span>
              <span className="text-xs text-muted-foreground">/ {tasks.length}</span>
            </div>
          </CardHeader>
          <CardContent className="flex flex-wrap gap-x-2.5 gap-y-0.5 text-[10px]">
            {(tasksByStatus.running ?? 0) > 0 && <span className="text-blue-400">探索 {tasksByStatus.running}</span>}
            {(tasksByStatus.paused ?? 0) > 0 && <span className="text-amber-400">暂停 {tasksByStatus.paused}</span>}
            {(tasksByStatus.done ?? 0) > 0 && <span className="text-emerald-400">完成 {tasksByStatus.done}</span>}
            {tasks.length === 0 && <span className="text-muted-foreground">暂无任务</span>}
          </CardContent>
        </Card>

        {/* 确认发现 */}
        <Card className="gap-1">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <BugIcon className="size-3" /> 确认发现
            </div>
            <div className="text-2xl font-semibold tabular-nums">{findings.length}</div>
          </CardHeader>
          <CardContent className="flex flex-wrap gap-2.5 text-[10px]">
            <span className="text-rose-500">严重 {findingsBySev.critical}</span>
            <span className="text-red-400">高危 {findingsBySev.high}</span>
            <span className="text-amber-400">中危 {findingsBySev.medium}</span>
            <span className="text-slate-400">低危 {findingsBySev.low}</span>
          </CardContent>
        </Card>

        {/* 资产节点 */}
        <Card className="gap-1">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <NetworkIcon className="size-3" /> 资产节点
            </div>
            <div className="text-2xl font-semibold tabular-nums">{totalAssets}</div>
          </CardHeader>
          <CardContent className="text-[10px] text-muted-foreground">跨任务共享</CardContent>
        </Card>

        {/* 流量交互 */}
        <Card className="gap-1">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <ActivityIcon className="size-3" /> 流量交互
            </div>
            <div className="text-2xl font-semibold tabular-nums">{traffic.length}</div>
          </CardHeader>
          <CardContent className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
            {settings?.traffic_capture ? (
              <>
                <LiveDot />
                <span>录制中</span>
              </>
            ) : (
              <span>捕获未开启</span>
            )}
          </CardContent>
        </Card>

        {/* LLM 用量 */}
        <Card className="gap-1">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <ZapIcon className="size-3" /> Token 用量
            </div>
            <div className="text-2xl font-semibold tabular-nums">
              {fmtTokens(displayedTokens.input + displayedTokens.output) || "—"}
            </div>
          </CardHeader>
          <CardContent className="text-[10px] text-muted-foreground">
            入 {fmtTokens(displayedTokens.input)}（含缓存 {fmtTokens(displayedTokens.cacheRead)}）· 出{" "}
            {fmtTokens(displayedTokens.output)}
          </CardContent>
        </Card>
      </div>

      {/* ── Row 2: LLM Token 消耗 ── */}
      <Card className="p-4">
        {/* Header */}
        <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-1.5 text-xs font-semibold">
            <ZapIcon className="size-3.5 text-muted-foreground" />
            LLM Token 消耗
            {/* 固定使用 llm_usage 计量账本：逐次调用精确、含中断消耗；只覆盖账本启用之后的调用。 */}
            <span
              className="ml-1 rounded-md border bg-muted/30 px-2 py-0.5 text-[10px] font-normal text-muted-foreground"
              title="数据来源：llm_usage 计量账本，按每次真实调用记账（含中断消耗）。仅统计账本启用之后的调用，历史 activity 统计已移除。"
            >
              llm_usage
            </span>
          </div>
          {/* Profile tabs */}
          <div className="flex flex-wrap items-center gap-1">
            <button
              type="button"
              onClick={() => setTokenTab("all")}
              className={cn(
                "rounded-md px-2.5 py-1 text-[10px] font-medium transition-colors",
                tokenTab === "all"
                  ? "bg-foreground text-background"
                  : "bg-muted/30 text-muted-foreground hover:text-foreground",
              )}
            >
              全部
            </button>
            {llmProfiles.map((p) => {
              const key = Number(p.id);
              const hasData = tokenByProfile.has(key) || (p.is_default && tokenByProfile.has(defaultProfileId));
              const resolvedKey = tokenByProfile.has(key) ? key : p.is_default ? defaultProfileId : key;
              return (
                <button
                  type="button"
                  key={p.id}
                  onClick={() => setTokenTab(resolvedKey)}
                  className={cn(
                    "flex items-center gap-1 rounded-md px-2.5 py-1 text-[10px] font-medium transition-colors",
                    tokenTab === resolvedKey
                      ? "bg-foreground text-background"
                      : "bg-muted/30 text-muted-foreground hover:text-foreground",
                    !hasData && "opacity-40",
                  )}
                >
                  {p.name}
                  {p.is_default && (
                    <span
                      className={cn(
                        "rounded px-1 py-0 text-[8px]",
                        tokenTab === resolvedKey
                          ? "bg-background/20 text-background"
                          : "bg-emerald-500/20 text-emerald-400",
                      )}
                    >
                      默认
                    </span>
                  )}
                </button>
              );
            })}
          </div>
        </div>

        {/* Body: metrics left + bar chart right */}
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-[220px_1fr]">
          {/* Left: key metrics */}
          <div className="flex flex-col gap-4">
            {/* Total */}
            <div>
              <div className="text-[10px] text-muted-foreground">累计 (输入+输出)</div>
              <div className="mt-0.5 text-3xl font-bold tabular-nums tracking-tight">
                {fmtTokens(displayedTokens.input + displayedTokens.output) || "—"}
              </div>
              <div className="mt-0.5 text-[10px] text-muted-foreground">{displayedTokens.taskCount} 个任务</div>
            </div>

            {/* Per-type bars */}
            <div className="space-y-3">
              {(() => {
                // input 已含缓存；拆成不重叠三段：未命中输入 + 缓存命中 + 输出 = 总量。
                const total = displayedTokens.input + displayedTokens.output;
                return [
                  {
                    label: "输入(未命中)",
                    value: displayedTokens.input - displayedTokens.cacheRead,
                    barColor: dailyTrendConfig.input.color!,
                    text: "text-blue-400",
                  },
                  {
                    label: "缓存命中",
                    value: displayedTokens.cacheRead,
                    barColor: dailyTrendConfig.cacheRead.color!,
                    text: "text-emerald-400",
                  },
                  {
                    label: "输出",
                    value: displayedTokens.output,
                    barColor: dailyTrendConfig.output.color!,
                    text: "text-violet-400",
                  },
                ].map(({ label, value, barColor, text }) => {
                  const pct = total > 0 ? (value / total) * 100 : 0;
                  return (
                    <div key={label}>
                      <div className="mb-1 flex items-center justify-between text-[10px]">
                        <span className="text-muted-foreground">{label}</span>
                        <span className={cn("font-mono font-semibold tabular-nums", text)}>{fmtTokens(value)}</span>
                      </div>
                      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                        <div
                          className="h-full rounded-full transition-all duration-500"
                          style={{ width: `${pct}%`, background: barColor }}
                        />
                      </div>
                    </div>
                  );
                });
              })()}
            </div>

            {/* Cache hit rate */}
            {(() => {
              // input 已含缓存 → 命中率 = 缓存命中 / 总输入。
              const denominator = displayedTokens.input;
              const hitPct = denominator > 0 ? Math.round((displayedTokens.cacheRead / denominator) * 100) : 0;
              return (
                <div className="flex items-center justify-between rounded-lg border bg-muted/20 px-3 py-2 text-[10px]">
                  <span className="text-muted-foreground">缓存命中率</span>
                  <span
                    className={cn("font-semibold tabular-nums", hitPct > 50 ? "text-emerald-400" : "text-amber-400")}
                  >
                    {hitPct}%
                  </span>
                </div>
              );
            })()}
          </div>

          {/* Right: daily bar chart */}
          <div className="flex min-h-0 flex-col">
            <div className="mb-2 flex items-center justify-between">
              <div className="flex gap-3 text-[9px] text-muted-foreground">
                {(["input", "output", "cacheRead"] as const).map((k) => (
                  <span key={k} className="flex items-center gap-1">
                    <span
                      className="inline-block size-2 rounded-sm"
                      style={{ background: dailyTrendConfig[k].color }}
                    />
                    {dailyTrendConfig[k].label}
                  </span>
                ))}
              </div>
              <div className="flex gap-0.5 rounded-md border bg-muted/30 p-0.5">
                {(
                  [
                    { value: "today", label: "今天" },
                    { value: 7, label: "7天" },
                    { value: 30, label: "30天" },
                    { value: 90, label: "3月" },
                    { value: 180, label: "6月" },
                    { value: 365, label: "一年" },
                  ] as const
                ).map(({ value, label }) => (
                  <button
                    type="button"
                    key={value}
                    onClick={() => setTokenRange(value)}
                    className={cn(
                      "rounded px-2.5 py-0.5 text-[9px] font-medium transition-colors",
                      tokenRange === value
                        ? "bg-background text-foreground shadow-sm"
                        : "text-muted-foreground hover:text-foreground",
                    )}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </div>

            {dailyTokenData.length === 0 ? (
              <div
                className="flex flex-1 items-center justify-center rounded-lg border bg-muted/10 text-xs text-muted-foreground"
                style={{ minHeight: 180 }}
              >
                暂无数据
              </div>
            ) : (
              <ChartContainer config={dailyTrendConfig} className="h-[200px] w-full">
                <BarChart data={dailyTokenData} margin={{ top: 4, right: 4, bottom: 0, left: 0 }} maxBarSize={40}>
                  <CartesianGrid vertical={false} />
                  <XAxis
                    dataKey="date"
                    tickLine={false}
                    axisLine={false}
                    tick={{ fontSize: 10 }}
                    tickMargin={6}
                    interval={tokenRange === "today" ? 2 : "preserveStartEnd"}
                  />
                  <YAxis
                    tickFormatter={(v) => fmtTokens(Number(v))}
                    tickLine={false}
                    axisLine={false}
                    tick={{ fontSize: 10 }}
                    width={44}
                  />
                  <ChartTooltip
                    cursor={false}
                    content={
                      <ChartTooltipContent
                        indicator="line"
                        formatter={(value, name) => (
                          <div className="flex w-full items-center justify-between gap-4">
                            <span>
                              {(dailyTrendConfig as Record<string, { label: string }>)[String(name)]?.label ??
                                String(name)}
                            </span>
                            <span className="font-mono font-semibold tabular-nums">{fmtTokens(Number(value))}</span>
                          </div>
                        )}
                      />
                    }
                  />
                  <Bar dataKey="input" stackId="1" fill="var(--color-input)" radius={[0, 0, 0, 0]} />
                  <Bar dataKey="output" stackId="1" fill="var(--color-output)" radius={[0, 0, 0, 0]} />
                  <Bar dataKey="cacheRead" stackId="1" fill="var(--color-cacheRead)" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ChartContainer>
            )}
          </div>
        </div>
      </Card>

      {/* ── Row 3: 网络实时监测 ── */}
      <Card className="overflow-hidden p-0">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex items-center gap-1.5 text-xs font-semibold">
              <WifiIcon className="size-3.5 text-muted-foreground" />
              网络实时监测
            </div>
            <Badge variant="outline" className={cn("gap-1.5 px-2 py-0 text-[10px]", networkMeta.badge)}>
              <span
                className={cn(
                  "size-1.5 rounded-full",
                  networkMeta.dot,
                  networkVisualState === "online" && "animate-pulse",
                )}
              />
              {networkMeta.label}
            </Badge>
            <span className="text-[10px] text-muted-foreground">
              每 {networkInterval} 秒采样 · 保留 {networkStatus?.monitor?.retention_days ?? 30} 天 · 本地时间
            </span>
          </div>
          <div className="flex items-center gap-1.5 text-[10px] tabular-nums text-muted-foreground">
            {networkStatus ? (
              networkStatus.last_outage ? (
                networkStatus.last_outage.ongoing ? (
                  <>
                    <span className="size-1.5 rounded-full bg-red-500" />
                    <span className="text-red-400">当前断网：自 {fmtClock(networkStatus.last_outage.down_at)} 起</span>
                    <span>· 已持续 {fmtDuration(networkStatus.last_outage.seconds)}</span>
                  </>
                ) : (
                  <>
                    <span className="size-1.5 rounded-full bg-muted-foreground/40" />
                    <span>最近断网：{fmtClock(networkStatus.last_outage.down_at)}</span>
                    <span>→ {fmtClock(networkStatus.last_outage.up_at)}</span>
                    <span>（{fmtDuration(networkStatus.last_outage.seconds)}）</span>
                  </>
                )
              ) : (
                <span>最近 7 天无断网记录</span>
              )
            ) : (
              <span>正在读取断网记录…</span>
            )}
          </div>
        </div>

        <div className="grid grid-cols-1 gap-4 p-4 lg:grid-cols-[290px_1fr]">
          <div className="flex flex-col gap-2.5">
            <div className={cn("rounded-xl border p-3", networkMeta.badge)}>
              <div className="flex items-center gap-3">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-current/15 bg-background/30">
                  <NetworkStatusIcon className="size-4" />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-semibold tracking-tight">{networkMeta.label}</div>
                  <div className="mt-0.5 truncate text-[10px] opacity-80">
                    {networkSample
                      ? `最新采样 ${fmtClock(networkSample.ts)} · ${fmtRel(networkSample.ts)}`
                      : "等待后台首轮探测结果"}
                  </div>
                </div>
                <div className="text-right">
                  <div className="font-mono text-lg font-semibold tabular-nums">
                    {networkSample ? networkSample.latency_ms : "—"}
                  </div>
                  <div className="text-[9px] opacity-70">峰值 ms</div>
                </div>
              </div>
              {networkStale && (
                <div className="mt-2 rounded-md border border-red-500/20 bg-red-500/10 px-2 py-1 text-[10px] text-red-400">
                  最新采样已超过 {fmtDuration(networkSampleAge ?? 0)}，监测循环可能已停止
                </div>
              )}
            </div>

            <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-3 lg:grid-cols-2">
              {networkChecks.map((check) => (
                <div
                  key={check.label}
                  title={check.err || `${check.label} ${check.ok === null ? "等待采样" : check.ok ? "通过" : "失败"}`}
                  className="flex min-w-0 items-center gap-1.5 rounded-md border bg-muted/15 px-2 py-1.5 text-[10px]"
                >
                  <span
                    className={cn(
                      "size-1.5 shrink-0 rounded-full",
                      check.ok === null ? "bg-muted-foreground/30" : check.ok ? "bg-emerald-400" : "bg-red-500",
                    )}
                  />
                  <span className="min-w-0 flex-1 truncate text-muted-foreground">{check.label}</span>
                  <span
                    className={cn(
                      "shrink-0 font-mono tabular-nums",
                      check.ok === null ? "text-muted-foreground/60" : check.ok ? "text-emerald-400" : "text-red-400",
                    )}
                  >
                    {check.ok === null ? "—" : check.ok ? `${check.ms ?? 0}ms` : "失败"}
                  </span>
                </div>
              ))}
            </div>

            <div className="flex items-center justify-between px-0.5 text-[9px] text-muted-foreground">
              <span>外部状态由 DNS / TCP / HTTP 判定</span>
              <span>{networkSample?.detail?.external_ok ?? 0}/3 通过</span>
            </div>
          </div>

          <div className="min-w-0">
            <div className="mb-1.5 flex flex-wrap items-center justify-between gap-2">
              <div className="text-[10px] font-medium text-muted-foreground">最近 24 小时连接状态</div>
              <div className="flex flex-wrap items-center gap-2.5 text-[9px] text-muted-foreground">
                <span className="flex items-center gap-1">
                  <span className="size-2 rounded-sm bg-emerald-500/80" />
                  在线
                </span>
                <span className="flex items-center gap-1">
                  <span className="size-2 rounded-sm bg-amber-400/80" />
                  波动
                </span>
                <span className="flex items-center gap-1">
                  <span className="size-2 rounded-sm bg-red-500" />
                  断网
                </span>
                <span className="flex items-center gap-1">
                  <span className="size-2 rounded-sm bg-muted-foreground/20" />
                  无采样
                </span>
              </div>
            </div>
            <div
              className="flex h-9 w-full items-stretch gap-px overflow-hidden rounded-md border bg-muted/10 p-1"
              role="img"
              aria-label="最近 24 小时网络状态，每格 5 分钟"
            >
              {networkBars.map((bar) => (
                <span
                  key={bar.ts}
                  title={`${fmtClock(bar.ts)} - ${fmtClockShort(bar.ts + 5 * 60 * 1000)} · ${
                    bar.state === "nodata"
                      ? "无采样"
                      : bar.state === "online"
                        ? "在线"
                        : bar.state === "degraded"
                          ? "波动"
                          : "断网"
                  }${bar.samples > 0 ? ` · ${bar.samples} 个采样 · ${bar.latency}ms` : ""}`}
                  className={cn(
                    "min-w-0 flex-1 rounded-[1px] transition-opacity hover:opacity-70",
                    bar.state === "online" && "bg-emerald-500/80",
                    bar.state === "degraded" && "bg-amber-400/85",
                    bar.state === "offline" && "bg-red-500",
                    bar.state === "nodata" && "bg-muted-foreground/15",
                  )}
                />
              ))}
            </div>
            {networkBars.length > 0 && (
              <div className="mt-1 flex justify-between px-0.5 text-[9px] tabular-nums text-muted-foreground">
                {[24, 18, 12, 6, 0].map((hoursAgo) => (
                  <span key={hoursAgo}>
                    {hoursAgo === 0 ? "现在" : fmtClockShort((networkBars.at(-1)?.ts ?? 0) - hoursAgo * 60 * 60 * 1000)}
                  </span>
                ))}
              </div>
            )}

            <div className="mt-2 flex flex-wrap items-center gap-1.5 border-t pt-2">
              <span className="mr-1 text-[9px] text-muted-foreground">近 24 小时断网记录</span>
              {networkHistory ? (
                recentNetworkOutages.length > 0 ? (
                  recentNetworkOutages.map((outage, index) => (
                    <span
                      key={`${outage.down_at}-${index}`}
                      className="rounded border border-red-500/20 bg-red-500/5 px-1.5 py-0.5 font-mono text-[9px] tabular-nums text-red-400"
                    >
                      {fmtClock(outage.down_at)} → {outage.ongoing ? "持续中" : fmtClock(outage.up_at)}（
                      {fmtDuration(outage.seconds)}）
                    </span>
                  ))
                ) : (
                  <span className="text-[9px] text-emerald-400/80">无断网</span>
                )
              ) : (
                <span className="text-[9px] text-muted-foreground">加载中…</span>
              )}
            </div>
          </div>
        </div>
      </Card>

      {/* ── Row 4: 活动流 | 发现 ── */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        {/* 活动流 */}
        <Card className="p-4">
          <div className="mb-3 flex items-center justify-between">
            <div className="flex items-center gap-1.5 text-xs font-semibold">
              <ActivityIcon className="size-3.5 text-muted-foreground" />
              活动流
            </div>
            <div className="flex items-center gap-2">
              <span className="text-[10px] text-muted-foreground">
                {activity.filter((a) => a.kind !== "usage").length} 条事件
              </span>
              <Link
                href="/function/tasks"
                className="flex items-center gap-0.5 text-[10px] text-muted-foreground hover:text-foreground"
              >
                查看任务 <ArrowUpRightIcon className="size-3" />
              </Link>
            </div>
          </div>

          <div className="mb-2.5 flex flex-wrap gap-1.5">
            {(["tool_use", "tool_result", "text", "thinking", "result"] as const).map((kind) => {
              const count = activity.filter((a) => a.kind === kind).length;
              if (count === 0) return null;
              return (
                <span key={kind} className="rounded border bg-muted/20 px-1.5 py-0.5 text-[9px] text-muted-foreground">
                  {kind} <strong className="text-foreground/70">{count}</strong>
                </span>
              );
            })}
          </div>

          <div className="divide-y">
            {recentActivity.length === 0 ? (
              <div className="py-4 text-center text-xs text-muted-foreground">暂无活动记录</div>
            ) : (
              recentActivity.map((a) => (
                <div key={a.seq} className="flex gap-2.5 py-2">
                  <span
                    className={cn(
                      "shrink-0 self-start rounded border px-1.5 py-0.5 font-mono text-[9px]",
                      workerBg(a.worker),
                    )}
                  >
                    {a.worker}
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="text-[11px] font-medium">{kindLabel(a)}</div>
                    <div className="mt-0.5 truncate text-[10px] text-muted-foreground">{a.summary}</div>
                  </div>
                  <div className="shrink-0 text-right">
                    <Badge variant="outline" className="px-1.5 py-0 text-[9px] text-muted-foreground">
                      {a.kind}
                    </Badge>
                    <div className="mt-0.5 text-[9px] tabular-nums text-muted-foreground">{fmtRel(a.ts)}</div>
                  </div>
                </div>
              ))
            )}
          </div>
        </Card>

        {/* 发现 */}
        <Card className="p-4">
          <div className="mb-3 flex items-center justify-between">
            <div className="flex items-center gap-1.5 text-xs font-semibold">
              <BugIcon className="size-3.5 text-muted-foreground" />
              发现
            </div>
            <Link
              href="/function/findings"
              className="flex items-center gap-0.5 text-[10px] text-muted-foreground hover:text-foreground"
            >
              全部 <ArrowUpRightIcon className="size-3" />
            </Link>
          </div>

          <div className="divide-y">
            {recentFindings.length === 0 ? (
              <div className="py-4 text-center text-xs text-muted-foreground">暂无发现</div>
            ) : (
              recentFindings.map((f) => (
                <div key={f.id} className="flex items-start gap-2 py-2">
                  <StatusBadge
                    domain="severity"
                    value={f.severity}
                    className="mt-0.5 shrink-0 px-1.5 py-0 text-[10px]"
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="text-[11px] font-semibold">{f.vulnclass}</span>
                      {f.task_description && (
                        <span className="truncate text-[10px] text-muted-foreground">{f.task_description}</span>
                      )}
                    </div>
                    <div className="mt-0.5 line-clamp-2 text-[10px] leading-snug text-muted-foreground">
                      {f.summary}
                    </div>
                  </div>
                  <span className="shrink-0 text-[9px] tabular-nums text-muted-foreground">{fmtRel(f.ts)}</span>
                </div>
              ))
            )}
          </div>
        </Card>
      </div>

      {/* ── Row 5: 任务表格 ── */}
      <Card className="overflow-hidden p-0">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <div className="flex items-center gap-1.5 text-xs font-semibold">
            <ClockIcon className="size-3.5 text-muted-foreground" />
            任务
          </div>
          <div className="flex items-center gap-2">
            <span className="text-[10px] text-muted-foreground">{tasks.length} 个任务</span>
            <Link
              href="/function/tasks"
              className="flex items-center gap-0.5 text-[10px] text-muted-foreground hover:text-foreground"
            >
              全部 <ArrowUpRightIcon className="size-3" />
            </Link>
          </div>
        </div>
        <table className="w-full border-collapse text-xs">
          <thead>
            <tr className="border-b">
              {["任务", "状态", "引擎", "目标进度", "在途", "最近活动"].map((h) => (
                <th
                  key={h}
                  className="px-4 py-2 text-left text-[9px] font-semibold uppercase tracking-widest text-muted-foreground first:pl-4"
                >
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sortedTasks.length === 0 ? (
              <tr>
                <td colSpan={6} className="px-4 py-6 text-center text-xs text-muted-foreground">
                  暂无任务
                </td>
              </tr>
            ) : (
              sortedTasks.map((t) => {
                const goalsPct = t.goals_total ? Math.round(((t.goals_met ?? 0) / t.goals_total) * 100) : null;
                return (
                  <tr key={t.id} className="border-b last:border-0 transition-colors hover:bg-muted/30">
                    <td className="max-w-xs px-4 py-3">
                      <Link href={`/function/tasks/detail?id=${t.id}`} className="group flex flex-col">
                        <span className="truncate font-medium group-hover:underline">{t.description}</span>
                        <span className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">{t.id}</span>
                      </Link>
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge domain="task" value={t.status} dot className="px-1.5 py-0 text-[10px]" />
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge
                        domain="engine"
                        value={t.engine_mode ?? "idle"}
                        className="px-1.5 py-0 text-[10px]"
                      />
                    </td>
                    <td className="px-4 py-3">
                      {goalsPct !== null ? (
                        <div className="flex items-center gap-2">
                          <Progress value={goalsPct} className="h-1 w-14" />
                          <span className="tabular-nums text-muted-foreground">
                            {t.goals_met ?? 0}/{t.goals_total}
                          </span>
                        </div>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </td>
                    <td className="px-4 py-3 tabular-nums">
                      {(t.in_flight ?? 0) > 0 ? (
                        <span className="font-semibold">{t.in_flight}</span>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </td>
                    <td className="px-4 py-3 tabular-nums text-muted-foreground">
                      {fmtRel(t.last_activity_unix ?? t.created_unix)}
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </Card>

      {/* ── Row 6: 资产分布 | 流量状态码 | 拦截 & 待审批 ── */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        {/* 资产分布 */}
        <Card className="p-4">
          <SectionTitle icon={NetworkIcon} sub="按类型">
            资产分布
          </SectionTitle>

          {assetByType.length === 0 ? (
            <div className="py-6 text-center text-xs text-muted-foreground">暂无资产数据</div>
          ) : (
            <div className="flex flex-col gap-2">
              {assetByType.map(([type, count]) => (
                <div key={type} className="flex items-center gap-2 text-xs">
                  <span className="w-8 shrink-0 text-right text-[10px] text-muted-foreground">
                    {ASSET_TYPE_LABELS[type] ?? type}
                  </span>
                  <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                    <div
                      className={cn("h-full rounded-full", ASSET_COLORS[type] ?? "bg-slate-400")}
                      style={{ width: `${(count / assetMax) * 100}%` }}
                    />
                  </div>
                  <span className="w-4 text-right tabular-nums">{count}</span>
                </div>
              ))}
            </div>
          )}

          <div className="mt-3 border-t pt-3 text-[10px] text-muted-foreground">共 {totalAssets} 节点</div>
        </Card>

        {/* 流量状态码 */}
        <Card className="p-4">
          <SectionTitle icon={ActivityIcon} sub={`${traffic.length} 次请求`}>
            流量状态码
          </SectionTitle>

          {/* bar chart */}
          {trafficByCodes.length === 0 ? (
            <div className="py-6 text-center text-xs text-muted-foreground">暂无流量数据</div>
          ) : (
            <>
              <div className="mb-3 flex items-end gap-2" style={{ height: 52 }}>
                {trafficByCodes.map(({ code, n }) => (
                  <div key={code} className="flex flex-1 flex-col items-center gap-1">
                    <span className="text-[9px] tabular-nums text-muted-foreground">{n}</span>
                    <div
                      className={cn("w-full min-h-1 rounded-sm", statusBg(code), "opacity-80")}
                      style={{ height: Math.max(4, (n / trafficMax) * 36) }}
                    />
                    <span className={cn("text-[9px] font-mono", statusColor(code))}>{code}</span>
                  </div>
                ))}
              </div>

              <div className="border-t pt-2.5">
                <div className="mb-1.5 text-[10px] text-muted-foreground">最近请求</div>
                <div className="flex flex-col gap-1.5">
                  {recentTraffic.map((e) => (
                    <div key={e.id} className="flex items-center gap-1.5 text-[10px]">
                      <span
                        className={cn(
                          "shrink-0 rounded border px-1 py-0 font-mono text-[9px]",
                          e.method === "GET"
                            ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-400"
                            : "border-blue-500/30 bg-blue-500/10 text-blue-400",
                        )}
                      >
                        {e.method}
                      </span>
                      <span className="min-w-0 flex-1 truncate text-muted-foreground">
                        {e.host}
                        {e.url.replace(/^https?:\/\/[^/]+/, "").substring(0, 30)}
                      </span>
                      <span className={cn("shrink-0 font-mono text-[9px]", statusColor(e.status))}>{e.status}</span>
                    </div>
                  ))}
                </div>
              </div>
            </>
          )}
        </Card>

        {/* 系统状态 & 待审批 */}
        <Card className="p-4">
          <SectionTitle icon={ShieldCheckIcon}>系统状态</SectionTitle>

          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between rounded-lg border bg-muted/20 px-3 py-2">
              <div className="text-[10px] text-muted-foreground">LLM 配置</div>
              <Badge
                variant="outline"
                className={cn(
                  "text-[10px]",
                  stats?.llm_configured
                    ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-400"
                    : "border-red-500/30 bg-red-500/10 text-red-400",
                )}
              >
                {stats?.llm_configured ? "已配置" : "未配置"}
              </Badge>
            </div>

            <div className="flex items-center justify-between rounded-lg border bg-muted/20 px-3 py-2">
              <div className="text-[10px] text-muted-foreground">流量捕获</div>
              <Badge
                variant="outline"
                className={cn(
                  "text-[10px]",
                  settings?.traffic_capture
                    ? "border-violet-500/30 bg-violet-500/10 text-violet-400"
                    : "text-muted-foreground",
                )}
              >
                {settings?.traffic_capture ? "开启" : "关闭"}
              </Badge>
            </div>

            {activeProfile && (
              <div className="flex items-center justify-between rounded-lg border bg-muted/20 px-3 py-2">
                <div className="text-[10px] text-muted-foreground">激活模型</div>
                <span className="font-mono text-[10px]">{activeProfile.model}</span>
              </div>
            )}
          </div>

          {/* pending approvals */}
          {pendingCount > 0 && (
            <div className="mt-3">
              <div className="mb-1.5 text-[10px] font-medium text-amber-400">待审批 ({pendingCount})</div>
              <div className="flex flex-col gap-1.5">
                {pending.slice(0, 3).map((p) => (
                  <Link
                    key={p.id}
                    href="/system/intercept/approvals"
                    className="flex items-center justify-between rounded-lg border border-amber-500/20 bg-amber-500/5 px-2.5 py-2 hover:bg-amber-500/10"
                  >
                    <div className="min-w-0">
                      <div className="text-[10px] font-medium">{p.tool_name}</div>
                      <div className="text-[9px] text-muted-foreground">{p.agent_name}</div>
                    </div>
                    <ArrowUpRightIcon className="size-3 shrink-0 text-amber-400" />
                  </Link>
                ))}
                {pendingCount > 3 && (
                  <Link
                    href="/system/intercept/approvals"
                    className="text-center text-[10px] text-muted-foreground hover:text-foreground"
                  >
                    还有 {pendingCount - 3} 条…
                  </Link>
                )}
              </div>
            </div>
          )}

          {pendingCount === 0 && (
            <div className="mt-3 rounded-lg border bg-muted/10 px-3 py-3 text-center text-[10px] text-muted-foreground">
              <ShieldCheckIcon className="mx-auto mb-1 size-4 text-emerald-500/50" />
              无待审批拦截
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
