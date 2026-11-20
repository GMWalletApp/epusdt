import {r as interop} from "./chunk-DECur_0Z.js";
import {a as queries, s as api} from "./auth-store-uaDKRR4l.js";
import {t as react} from "./react-CO2uhaBc.js";

const React = interop(react(), 1);
const h = React.createElement;
const base = "/admin/api/v1/chainscanner/admin";
const refresh = {refetchInterval: 30000, staleTime: 10000, retry: false};
const button = "rounded border px-2 py-1 text-sm disabled:opacity-50";
const panel = "rounded-md border bg-card p-4 space-y-2";
function label(zh, en) {
  return (document.documentElement.lang || navigator.language).startsWith("zh") ? zh : en;
}
function table(headers, rows) {
  const cell = "px-2 py-1 text-start whitespace-nowrap";
  return h("div", {className: "overflow-auto max-h-72"}, h("table", {className: "w-full text-sm"},
    h("thead", null, h("tr", null, headers.map(v => h("th", {key:v, scope:"col", className:cell}, v)))),
    h("tbody", null, rows.map(([key, ...cells]) => h("tr", {key}, cells.map((v,i) =>
      h("td", {key:headers[i], className:cell}, v)))))));
}
function queryState(query) {
  if (query.isError) return h("p", {role:"alert"}, label("无法读取 ChainScanner 数据，请检查连接和管理员令牌。", "ChainScanner data is unavailable. Check the connection and admin token."));
  if (query.isLoading) return h("p", null, label("正在读取…", "Loading…"));
  if (!Array.isArray(query.data)) return h("p", {role:"alert"}, label("节点服务未返回列表。", "The scanner did not return a list."));
  if (!query.data.length) return h("p", null, label("暂无数据", "No data"));
  return null;
}
export function ScanProgress() {
  const query = queries.useQuery("get", base + "/progress", {}, refresh);
  const states = {ok:label("正常","OK"), warning:label("警告：落后超过 5 分钟","Warning: more than 5 min behind"),
    idle:label("无订阅","Idle"), warming:label("等待样本","Waiting for a sample"), stale:label("缺少近期链头","No recent head")};
  return h("section", {className:panel}, h("h2", {className:"font-semibold"}, label("追块延迟","Scan lag")),
    h("p", {className:"text-sm text-muted-foreground"}, label("从扫描器观测到可扫高度起，到连续扫完该高度为止。超过 5 分钟告警。","Time since the scanner first observed an eligible height still ahead of its contiguous cursor. A warning appears after 5 minutes.")),
    queryState(query) || table([label("链","Network"),label("可扫高度","Target"),label("连续扫完高度","Scanned through"),label("已观测延迟","Observed lag"),label("状态","Status")],
      query.data.map(r => [r.network,r.network,r.target_height ?? "—",r.cursor_height ?? "—",
        r.lag_ms == null ? "—" : Math.floor(r.lag_ms / 1000) + " s",
        h("span",{className:r.status === "warning" || r.status === "stale" ? "font-semibold text-destructive" : ""},states[r.status] ?? r.status)])));
}
function hostname(raw) {
  try {return new URL(raw).hostname || "—";} catch {return "—";}
}
export function ScannerNodes({network}) {
  const [open,setOpen] = React.useState(true);
  const [busy,setBusy] = React.useState(false);
  const [error,setError] = React.useState("");
  const [selected,setSelected] = React.useState("");
  const scope = network && network !== "overview" ? network : "";
  const filter = scope || selected;
  const params = scope ? {params:{query:{network:scope}}} : {};
  const query = queries.useQuery("get",base+"/nodes",params,{...refresh,enabled:open});
  async function test(id) {
    setBusy(true);
    setError("");
    try {
      const result = id == null
        ? await api.POST(base+"/nodes/test",{params:{query:{network:filter}}})
        : await api.POST(base+"/nodes/{id}/test",{params:{path:{id}}});
      if (!result.response.ok) throw new Error(String(result.response.status));
      await query.refetch();
    } catch {
      setError(label("复测请求失败，请稍后重试。","Node test request failed. Try again."));
    } finally {setBusy(false);}
  }
  const inventory = Array.isArray(query.data) ? query.data : [];
  const networks = [...new Set(inventory.map(row=>row.network))].sort();
  const rows = filter ? inventory.filter(row=>row.network===filter) : inventory;
  return h("details",{open:open,className:panel,onToggle:e=>setOpen(e.currentTarget.open)},
    h("summary",{className:"cursor-pointer font-semibold"},label("ChainScanner 扫块节点","ChainScanner scan nodes")),
    open && h(React.Fragment,null,
      h("div",{className:"flex items-center gap-2"},
        !scope && h("select",{"aria-label":label("扫描网络","Scanner network"),className:button,value:selected,disabled:busy,onChange:e=>setSelected(e.target.value)},
          h("option",{value:""},label("所有扫描网络","All scanner networks")),networks.map(n=>h("option",{key:n,value:n},n))),
        h("button",{className:button,disabled:busy || query.isFetching,onClick:()=>query.refetch()},label("刷新列表","Refresh list")),
        h("button",{className:button,disabled:busy || !filter || !rows.length,onClick:()=>test(null)},label("复测所选链","Test selected network")),
        busy && h("span",{role:"status"},label("正在复测…","Testing…"))),
      error && h("p",{role:"alert"},error),
      queryState(query) || table([label("链","Network"),label("节点","Node"),label("状态","Status"),label("节点高度","Node height"),label("延时","Latency"),label("最近检测","Last checked"),label("操作","Action")],
        rows.map(n=>[n.id,n.network,hostname(n.url)+" · "+n.protocol+" #"+n.id,n.status+(n.read_retry_at_ms>Date.now()?label("（冷却中）"," (cooldown)"):""),
          n.height==null ? "—" : n.height+" "+(n.height_unit??""),n.latency_ms==null?"—":n.latency_ms+" ms",
          n.checked_at_ms==null?"—":new Date(n.checked_at_ms).toLocaleString(),
          h("button",{className:button,disabled:busy,onClick:()=>test(n.id)},label("复测","Test"))]))));
}
