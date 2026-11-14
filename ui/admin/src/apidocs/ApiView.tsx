import { useSearchState } from "../app/search";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useSmallScreen } from "../app/shell";
import { Facts } from "../components/Facts";
import { ErrorState } from "../components/Feedback";
import { authCollectionsQuery } from "../auth/queries";
import { getCollectionRules, getCollectionUserAccess, type Collection } from "../api/collections";
import { stringifyJson } from "../api/http";
import { CodeBlock } from "../components/CodeBlock";
import { CopyButton } from "../components/CopyButton";
import { SplitView, PaneHeader } from "../components/SplitView";
import { Tabs } from "../components/Tabs";
import { useT, type MessageKey } from "../i18n";
import { draftFromRule } from "../rules/model";
import { rulesQueryKey } from "../rules/useRulesEditor";
import { buildEndpoints, type Audience, type Endpoint } from "./endpoints";
import { codeSample, languages, type Language } from "./samples";
import "../styles/apidocs.css";

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

function EndpointPane({ endpoint, collection, audience, language, ruleText, onLanguage, onClose }: { endpoint: Endpoint; collection: Collection; audience: Audience; language: Language; ruleText: string; onLanguage: (language: Language) => void; onClose: () => void }) {
  const t = useT();
  const origin = window.location.origin;
  const sample = codeSample(language, endpoint, collection, origin, audience);
  return (
    <>
      <PaneHeader onClose={onClose} title={t(`api.endpoint.${endpoint.id.startsWith("files-") ? "files" : endpoint.id}` as MessageKey)} />
      <div className="pane-body api-pane">
        <div className="api-route">
          <span className="api-method" data-method={endpoint.method}>{endpoint.method}</span>
          <code className="mono">{endpoint.path}</code>
          <CopyButton label={t("api.copy")} value={origin + endpoint.path} />
        </div>
        <Facts items={[
          { label: t("api.rule"), value: ruleText, mono: Boolean(endpoint.rule) },
          { label: t("api.auth"), value: t(endpoint.userOnly || (audience === "user" && endpoint.rule) ? "api.auth.bearer" : "api.auth.none") },
        ]} />
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
  const { params, update } = useSearchState();
  const policySource = collection.type === "view" && !collection.view?.sql ? collection.view?.source ?? collection.name : collection.name;
  const rules = useQuery({ queryKey: rulesQueryKey(policySource), queryFn: ({ signal }) => getCollectionRules(policySource, signal) });
  const access = useQuery({ queryKey: [...rulesQueryKey(policySource), "access"], queryFn: ({ signal }) => getCollectionUserAccess(policySource, signal) });
  const audience: Audience = params.get("as") === "user" ? "user" : "guest";
  const language = (languages as readonly string[]).includes(params.get("lang") ?? "") ? (params.get("lang") as Language) : "curl";
  const realms = useQuery({ ...authCollectionsQuery, enabled: collection.type === "auth" });
  const endpoints = buildEndpoints(collection, audience, Boolean(realms.data?.some((realm) => realm.name === collection.name)));
  // No ?endpoint= means the default: "list" beside the list on desktop, nothing over it on small screens or once closed.
  const small = useSmallScreen();
  const [closed, setClosed] = useState(false);
  const selected = endpoints.find((endpoint) => endpoint.id === (params.get("endpoint") ?? (small || closed ? undefined : "list")));
  const set = (key: string, value: string | null) => update({ [key]: value }, { replace: true });
  const close = () => { setClosed(true); set("endpoint", null); };

  const ruleText = (endpoint: Endpoint) => {
    if (endpoint.id === "batch") return t("api.rule.perOperation");
    if (!endpoint.rule) return t(endpoint.userOnly ? "api.rule.session" : "api.rule.open");
    const rule = rules.data?.rules[endpoint.rule];
    if (!rule) return rules.isError ? "—" : "…";
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
        {(rules.isError || access.isError) && <ErrorState query={rules.isError ? rules : access} />}
        <ul className="api-list">
          {endpoints.map((endpoint) => (
            <li key={endpoint.id}>
              <button type="button" className="row-btn api-row" aria-current={selected?.id === endpoint.id} onClick={() => set("endpoint", endpoint.id)}>
                <span className="api-method" data-method={endpoint.method}>{endpoint.method}</span>
                <span className="api-path mono">{endpoint.path}</span>
                <span className="api-row-rule">{ruleText(endpoint)}</span>
              </button>
            </li>
          ))}
        </ul>
        <section className="api-syntax">
          <h2>{t("api.filterTitle")}</h2>
          <Facts items={filterSyntax.map(([example, key]) => ({ label: <span className="mono">{example}</span>, value: t(key) }))} />
        </section>
      </div>
    </>
  );
  const pane = selected ? (
    <EndpointPane onClose={close} endpoint={selected} collection={collection} audience={audience} language={language} ruleText={ruleText(selected)} onLanguage={(value) => set("lang", value === "curl" ? null : value)} />
  ) : null;
  return <SplitView list={list} pane={pane} paneLabel={t("api.detail")} onClose={close} />;
}
