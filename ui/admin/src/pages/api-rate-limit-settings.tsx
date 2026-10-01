import type { AdminSettings, APIRateLimitRule } from "../backend/ports";

export function ruleErrors(
  rules: APIRateLimitRule[],
  excludedIPs: string[],
): string | null {
  if (rules.length > 100 || excludedIPs.length > 100)
    return "Use at most 100 API rules and 100 excluded IP ranges.";
  for (const rule of rules) {
    const label = rule.label;
    if (
      label.length < 1 ||
      new TextEncoder().encode(label).length > 256 ||
      !/^(?:[A-Z]+ )?\/[^\p{White_Space}\p{Cc}?#]*$/u.test(label)
    )
      return "Use a route pattern or METHOD /pattern; a trailing / matches a prefix. Omit surrounding spaces, query strings, and fragments.";
    if (
      !Number.isInteger(rule.duration) ||
      rule.duration < 1 ||
      rule.duration > 86400
    )
      return "Rule duration must be between 1 and 86,400 seconds.";
    if (
      !Number.isInteger(rule.maxRequests) ||
      rule.maxRequests < 1 ||
      rule.maxRequests > 100000
    )
      return "Rule request limit must be between 1 and 100,000.";
  }
  return null;
}

export function APIRateLimitSettings({
  value,
  change,
  disabled,
}: {
  value: AdminSettings;
  change(patch: Partial<AdminSettings["rateLimits"]>): void;
  disabled: boolean;
}) {
  const rules = Array.isArray(value.rateLimits.rules)
    ? value.rateLimits.rules
    : [];
  const excludedIPs = Array.isArray(value.rateLimits.excludedIPs)
    ? value.rateLimits.excludedIPs
    : [];
  const changeRule = (index: number, patch: Partial<APIRateLimitRule>) =>
    change({
      rules: rules.map((rule, candidate) =>
        candidate === index ? { ...rule, ...patch } : rule,
      ),
    });
  return (
    <fieldset disabled={disabled}>
      <legend>API request rate limits</legend>
      <p>
        Labels match registered route patterns, not individual record URLs.
        Use /api/v1/collections/{"{collection}"}/records for administrator lists,
        /api/v1/user/collections/{"{collection}"}/records for user lists,
        or a prefix ending in /. Method rules take precedence;
        the first matching rule is used within each group. Limits are counted
        per caller and client IP for the selected audience.
      </p>
      <label>
        <input
          checked={value.rateLimits.enabled}
          type="checkbox"
          onChange={(event) => change({ enabled: event.target.checked })}
        />{" "}
        Enable API request rate limits
      </label>
      {rules.map((rule, index) => (
        <fieldset key={index}>
          <legend>Rule {index + 1}</legend>
          <label>
            Rule {index + 1} label
            <input
              value={rule.label}
              maxLength={256}
              placeholder="GET /api/items or /api/items/"
              onChange={(event) =>
                changeRule(index, { label: event.target.value })
              }
            />
          </label>
          <label>
            Rule {index + 1} audience
            <select
              value={rule.audience}
              onChange={(event) =>
                changeRule(index, {
                  audience: event.target.value as APIRateLimitRule["audience"],
                })
              }
            >
              <option value="">All callers</option>
              <option value="@guest">Guests</option>
              <option value="@auth">Signed-in callers</option>
            </select>
          </label>
          <label>
            Rule {index + 1} duration seconds
            <input
              type="number"
              min="1"
              max="86400"
              value={rule.duration}
              onChange={(event) =>
                changeRule(index, { duration: Number(event.target.value) })
              }
            />
          </label>
          <label>
            Rule {index + 1} maximum requests
            <input
              type="number"
              min="1"
              max="100000"
              value={rule.maxRequests}
              onChange={(event) =>
                changeRule(index, { maxRequests: Number(event.target.value) })
              }
            />
          </label>
          <button
            type="button"
            onClick={() =>
              change({
                rules: rules.filter((_, candidate) => candidate !== index),
              })
            }
          >
            Remove rule {index + 1}
          </button>
        </fieldset>
      ))}
      <button
        type="button"
        disabled={rules.length >= 100}
        onClick={() =>
          change({
            rules: [
              ...rules,
              { label: "", audience: "@guest", duration: 60, maxRequests: 100 },
            ],
          })
        }
      >
        Add API rate rule
      </button>
      <label>
        Excluded client IPs or CIDRs, one per line
        <textarea
          value={excludedIPs.join("\n")}
          onChange={(event) =>
            change({
              excludedIPs: event.target.value
                .split("\n")
                .map((item) => item.trim())
                .filter(Boolean),
            })
          }
        />
      </label>
    </fieldset>
  );
}
