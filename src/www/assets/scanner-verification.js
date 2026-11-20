// This panel uses the same authenticated settings hooks as the system form.
import {r as interop} from "./chunk-DECur_0Z.js";
import {s as adminAPI} from "./auth-store-uaDKRR4l.js";
import {t as react} from "./react-CO2uhaBc.js";
import {o as useSettings, s as useSaveSettings, t as setting} from "./lib-DyUkl8FG.js";

const React = interop(react(), 1);
const key = "system.strict_payment_verification";

export function ScannerVerification() {
  const query = useSettings("system");
  const mutation = useSaveSettings();
  const [message, setMessage] = React.useState("");
  const zh = (document.documentElement.lang || navigator.language).startsWith("zh");
  const rows = query.data?.data;
  const enabled = setting(rows, key, "true") !== "false";
  async function change(event) {
    const value = event.target.checked;
    setMessage("");
    try {
      const result = await mutation.mutateAsync({body: {items: [{group: "system", key, type: "bool", value}]}});
      if (!result.data?.some(row => row.key === key && row.ok)) throw new Error();
      await query.refetch();
    } catch {
      setMessage(zh ? "保存失败，请重试。" : "Could not save. Try again.");
    }
  }
  return React.createElement("section", {className: "mt-6 space-y-2"},
    React.createElement("label", {className: "flex items-center gap-2"},
      React.createElement("input", {type: "checkbox", checked: enabled,
        disabled: !rows || query.isFetching || mutation.isPending, onChange: change}),
      zh ? "链上到账前严格验证" : "Require strict verification before crediting payments"),
    React.createElement("p", {className: "text-muted-foreground text-sm"},
      zh ? "开启时，须由两个独立节点核实交易。关闭后，新收到的候选交易按订阅证据和订单规则入账。" :
      "Require agreement from two independent nodes. Turning this off lets new candidates settle from subscription evidence after order checks."),
    message && React.createElement("p", {role: "alert"}, message),
    React.createElement(Blacklist, {zh})
  );
}

function Blacklist({zh}) {
  const h = React.createElement;
  const [rows, setRows] = React.useState([]);
  const [rule, setRule] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const path = "/admin/api/v1/chainscanner/admin/blacklist";
  async function refresh(method, entry) {
    setBusy(true);
    setError("");
    try {
      if (method) {
        const result = await adminAPI[method](path + "/{rule}", {
          params: {path: {rule: entry}}, parseAs: "text"
        });
        if (!result.response.ok) throw new Error(String(result.error || result.response.status));
        setRule("");
      }
      const result = await adminAPI.GET(path);
      if (!result.response.ok || !Array.isArray(result.data)) throw new Error(String(result.response.status));
      setRows(result.data);
    } catch (failure) {
      setError((zh ? "节点黑名单请求失败 " : "Node blacklist request failed ") + failure.message);
    } finally {
      setBusy(false);
    }
  }
  return h("details", {onToggle: e => {if (e.currentTarget.open) refresh();}},
    h("summary", {className: "cursor-pointer"}, zh ? "RPC 节点地址黑名单" : "RPC node blacklist"),
    h("form", {className: "mt-2 flex gap-2", onSubmit: e => {e.preventDefault(); refresh("PUT", rule.trim());}},
      h("input", {className: "border rounded px-2", "aria-label": zh ? "域名或关键词规则" : "Hostname or keyword rule",
        placeholder: "exact:rpc.example.org / keyword:acme", value: rule, onChange: e => setRule(e.target.value)}),
      h("button", {disabled: busy || !rule.trim()}, zh ? "添加" : "Add")),
    h("ul", null, rows.map(row => h("li", {key: row.host, className: "flex items-center gap-2"},
      h("code", null, row.host), row.source === "config" ? (zh ? "配置文件" : "Configuration file") :
      h("button", {type: "button", disabled: busy, onClick: () => refresh("DELETE", row.host)}, zh ? "移除" : "Remove")))),
    error && h("p", {role: "alert"}, error));
}
