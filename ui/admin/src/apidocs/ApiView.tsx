import { useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { RiFileCopyLine } from "@remixicon/react";
import { listAuthCollections } from "../api/authCollections";
import { getCollectionRules, getCollectionUserAccess, type Collection } from "../api/collections";
import { stringifyJson } from "../api/http";
import { IconButton } from "../components/Button";
import { SplitView } from "../components/SplitView";
import { Tabs } from "../components/Tabs";
import { toast } from "../components/Toast";
import { useT, type MessageKey, type Translate } from "../i18n";
import { draftFromRule } from "../rules/model";
import { rulesQueryKey } from "../rules/useRulesEditor";
import { buildEndpoints, type Audience, type Endpoint } from "./endpoints";
import { codeSample, languages, type Language } from "./samples";
import "../styles/rules.css";

const filterSyntax: [string, MessageKey][] = [
  ["title = 'a' && count > 2", "api.filter.compare"],
  ["(a = 1 || b = 2) && c != 3", "api.filter.group"],
  ["title ~ 'draft'", "api.filter.contains"],
  ["title:lower ~ 'draft'", "api.filter.lower"],
  ["created_at >= '2026-01-01T00:00:00Z'", "api.filter.system"],
  ["geoDistance(place.lon, place.lat, 126.9, 37.5) < 5", "api.filter.geo"],
  ["strftime('%Y', created_at) = '2026'", "api.filter.time"],
  ["sort=-created_at,title", "api.filter.sort"],
  ["expand=author", "api.filter.expand"],
  ["limit=20&cursor=…", "api.filter.cursor"],
];

function copy(t: Translate, text: string) {
  void navigator.clipboard.writeText(text).then(
    () => toast(t("api.copied")),
    () => toast(t("api.copyFailed"), "danger"),
  );
}

function CodeBlock({ text, label }: { text: string; label: string }) {
  const t = useT();
  return (
    <div className="api-code">
      <pre className="mono" aria-label={label}>{text}</pre>
      <IconButton label={t("api.copy")} icon={RiFileCopyLine} onClick={() => copy(t, text)} />
    </div>
  );
}

function EndpointPane({ endpoint, collection, audience, language, ruleText, onLanguage }: { endpoint: Endpoint; collection: Collection; audience: Audience; language: Language; ruleText: string; onLanguage: (language: Language) => void }) {
  const t = useT();
  const origin = window.location.origin;
  const sample = codeSample(language, endpoint, collection, origin, audience);
  return (
    <>
      <header className="pane-header">
        <h2 className="pane-title">{t(`api.endpoint.${endpoint.id.startsWith("files-") ? "files" : endpoint.id}` as MessageKey)}</h2>
      </header>
      <div className="pane-body api-pane">
        <div className="api-route">
          <span className="api-method" data-method={endpoint.method}>{endpoint.method}</span>
          <code className="mono">{endpoint.path}</code>
          <IconButton label={t("api.copy")} icon={RiFileCopyLine} onClick={() => copy(t, origin + endpoint.path)} />
        </div>
        <dl className="api-facts">
          <div><dt>{t("api.rule")}</dt><dd className={endpoint.rule ? "mono" : undefined}>{ruleText}</dd></div>
          <div><dt>{t("api.auth")}</dt><dd>{t(endpoint.userOnly || (audience === "user" && endpoint.rule) ? "api.auth.bearer" : "api.auth.none")}</dd></div>
        </dl>
        {endpoint.query && (
          <section>
            <h3>{t("api.query")}</h3>
            <CodeBlock label={t("api.query")} text={Object.entries(endpoint.query).map(([key, value]) => `${key}=${value}`).join("\n")} />
          </section>
        )}
        {endpoint.headers && (
          <section>
            <h3>{t("api.headers")}</h3>
            <CodeBlock label={t("api.headers")} text={Object.entries(endpoint.headers).map(([key, value]) => `${key}: ${value}`).join("\n")} />
          </section>
        )}
        {endpoint.body && (
          <section>
            <h3>{t("api.request")}</h3>
            <CodeBlock label={t("api.request")} text={JSON.stringify(endpoint.body, null, 2)} />
          </section>
        )}
        <section>
          <h3>{t("api.response", { status: endpoint.status })}</h3>
          {endpoint.response !== undefined ? (
            <CodeBlock label={t("api.response", { status: endpoint.status })} text={JSON.stringify(endpoint.response, null, 2)} />
          ) : (
            <p className="field-hint">{t(endpoint.id === "realtime" ? "api.realtimeHint" : "api.noBody")}</p>
          )}
        </section>
        <section>
          <Tabs label={t("api.language")} value={language} onChange={onLanguage} tabs={languages.map((value) => ({ value, label: t(`api.lang.${value}`) }))} />
          {language !== "curl" && <p className="field-hint">{t("api.sdkHint")}</p>}
          <CodeBlock label={t("api.sample")} text={sample} />
        </section>
      </div>
    </>
  );
}

/** The API tab: a reference generated from this collection's schema and current rules. */
export function ApiView({ collection }: { collection: Collection }) {
  const t = useT();
  const [params, setParams] = useSearchParams();
  const rules = useQuery({ queryKey: rulesQueryKey(collection.name), queryFn: ({ signal }) => getCollectionRules(collection.name, signal) });
  const access = useQuery({ queryKey: [...rulesQueryKey(collection.name), "access"], queryFn: ({ signal }) => getCollectionUserAccess(collection.name, signal) });
  const audience: Audience = params.get("as") === "user" ? "user" : "guest";
  const language = (languages as readonly string[]).includes(params.get("lang") ?? "") ? (params.get("lang") as Language) : "curl";
  const realms = useQuery({ queryKey: ["auth-collections"], queryFn: ({ signal }) => listAuthCollections(signal), enabled: collection.type === "auth" });
  const endpoints = buildEndpoints(collection, audience, Boolean(realms.data?.some((realm) => realm.name === collection.name)));
  const selected = endpoints.find((endpoint) => endpoint.id === (params.get("endpoint") ?? "list"));
  const set = (key: string, value: string | null) =>
    setParams((current) => {
      const next = new URLSearchParams(current);
      if (value === null) next.delete(key);
      else next.set(key, value);
      return next;
    }, { replace: true });

  const ruleText = (endpoint: Endpoint) => {
    if (endpoint.id === "batch") return t("api.rule.perOperation");
    if (!endpoint.rule) return t(endpoint.userOnly ? "api.rule.session" : "api.rule.open");
    const rule = rules.data?.rules[endpoint.rule];
    if (!rule) return "…";
    if (access.data && !access.data.enabled) return t("api.rule.gateOff");
    const draft = draftFromRule(rule);
    return draft.mode === "expression" ? draft.expression : draft.mode === "custom" ? stringifyJson(rule) : t(`rules.mode.${draft.mode}`);
  };

  const list = (
    <>
      <div className="toolbar">
        <Tabs label={t("api.audience")} value={audience} onChange={(value) => set("as", value === "guest" ? null : value)} tabs={[{ value: "guest", label: t("api.as.guest") }, { value: "user", label: t("api.as.user") }]} />
        {access.data && !access.data.enabled && <span className="api-gate">{t("api.gateOff")}</span>}
      </div>
      <div className="page-content api-content">
        <ul className="api-list">
          {endpoints.map((endpoint) => (
            <li key={endpoint.id}>
              <button type="button" className="api-row" aria-current={selected?.id === endpoint.id} onClick={() => set("endpoint", endpoint.id)}>
                <span className="api-method" data-method={endpoint.method}>{endpoint.method}</span>
                <span className="api-path mono">{endpoint.path}</span>
                <span className="api-row-rule">{ruleText(endpoint)}</span>
              </button>
            </li>
          ))}
        </ul>
        <section className="api-syntax">
          <h2>{t("api.filterTitle")}</h2>
          <dl>
            {filterSyntax.map(([example, key]) => (
              <div key={example}>
                <dt className="mono">{example}</dt>
                <dd>{t(key)}</dd>
              </div>
            ))}
          </dl>
        </section>
      </div>
    </>
  );
  const pane = selected ? (
    <EndpointPane endpoint={selected} collection={collection} audience={audience} language={language} ruleText={ruleText(selected)} onLanguage={(value) => set("lang", value === "curl" ? null : value)} />
  ) : null;
  return <SplitView list={list} pane={pane} paneLabel={t("api.detail")} onClose={() => set("endpoint", "none")} />;
}
